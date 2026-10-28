package docker

import (
	"context"
	"errors"
	"fmt"
	"github.com/red-copilot/harness/internal/naming"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/red-copilot/harness"
)

// Runner starts agent processes through the Docker CLI. It deliberately keeps
// the container boundary in one place so tests and future runtime backends can
// swap the implementation without changing Pi or the runner.
type Runner struct {
	Docker string
	// Owner scopes orphan cleanup to this harness deployment. Two processes
	// sharing one Docker daemon must not reclaim each other's live Targets.
	// Empty means every labeled resource, which is only safe in tests.
	Owner string
}

// New returns the production starter and cleaner. The owner defaults to the
// host name so backends constructed elsewhere agree on the same label.
func New() *Runner { return &Runner{Docker: "docker", Owner: defaultOwner()} }

func (r *Runner) docker() string {
	if r != nil && r.Docker != "" {
		return r.Docker
	}
	return "docker"
}

// BuildRunArgs is the string form used by tests and diagnostics. Start uses
// BuildRunArgsChecked so malformed specs fail closed.
func BuildRunArgs(spec harness.ProcessSpec) []string {
	args, _ := BuildRunArgsChecked(spec)
	return args
}

func BuildRunArgsChecked(spec harness.ProcessSpec) ([]string, error) {
	if spec.Image == "" {
		return nil, errors.New("container image is required")
	}
	args := []string{"run", "--rm", "-i"}
	if spec.Name != "" {
		args = append(args, "--name", spec.Name)
	}
	labels := make(map[string]string, len(spec.Labels)+1)
	for key, value := range spec.Labels {
		labels[key] = value
	}
	labels["harness.managed"] = "true"
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--label", key+"="+labels[key])
	}
	if spec.Network != "" {
		args = append(args, "--network", spec.Network)
	}
	if spec.ReadOnlyRoot {
		args = append(args, "--read-only")
	}
	args = append(args, "--tmpfs", "/tmp:rw,nosuid,nodev,size=256m")
	args = append(args, "--cap-drop", "ALL")
	for _, capability := range spec.CapAdd {
		args = append(args, "--cap-add", capability)
	}
	args = append(args, "--security-opt", "no-new-privileges")
	if spec.PidsLimit > 0 {
		args = append(args, "--pids-limit", fmt.Sprintf("%d", spec.PidsLimit))
	}
	if spec.Memory != "" {
		args = append(args, "--memory", spec.Memory)
	}
	if spec.CPUs != "" {
		args = append(args, "--cpus", spec.CPUs)
	}
	for _, mount := range spec.Mounts {
		if mount.Source == "" || mount.Destination == "" {
			return nil, errors.New("mount source and destination are required")
		}
		if err := validateAgentMount(mount); err != nil {
			return nil, err
		}
		value := mount.Source + ":" + mount.Destination
		if mount.ReadOnly {
			value += ":ro"
		}
		args = append(args, "-v", value)
	}
	envKeys := make([]string, 0, len(spec.Env))
	for key := range spec.Env {
		envKeys = append(envKeys, key)
	}
	sort.Strings(envKeys)
	for _, key := range envKeys {
		// Do not put secret values in argv. The Docker CLI inherits values
		// from its own environment and forwards the named variables.
		args = append(args, "-e", key)
	}
	if spec.WorkingDir != "" {
		args = append(args, "-w", spec.WorkingDir)
	}
	if spec.User != "" {
		args = append(args, "--user", spec.User)
	}
	args = append(args, spec.Image)
	args = append(args, spec.Command...)
	return args, nil
}

func validateAgentMount(mount harness.Mount) error {
	source := filepath.Clean(mount.Source)
	destination := filepath.Clean(mount.Destination)
	if filepath.Base(source) == "docker.sock" || filepath.Base(destination) == "docker.sock" {
		return errors.New("docker control socket mounts are forbidden")
	}
	for _, forbidden := range []string{"/var/run", "/run"} {
		if source == forbidden || destination == forbidden {
			return fmt.Errorf("mounting runtime directory %s is forbidden", forbidden)
		}
	}
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("resolve mount source: %w", err)
	}
	if filepath.Base(resolved) == "docker.sock" {
		return errors.New("docker control socket mounts are forbidden")
	}
	for _, forbidden := range []string{"/var/run", "/run"} {
		if resolved == forbidden {
			return fmt.Errorf("mounting runtime directory %s is forbidden", forbidden)
		}
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("stat mount source: %w", err)
	}
	if info.Mode()&os.ModeSocket != 0 {
		return errors.New("mounting Unix sockets is forbidden")
	}
	if info.IsDir() && containsDockerSocketPath(resolved) {
		return errors.New("mounts containing a docker control socket are forbidden")
	}
	return nil
}

func containsDockerSocketPath(root string) bool {
	candidates := []string{"/var/run/docker.sock", "/run/docker.sock"}
	if host := os.Getenv("DOCKER_HOST"); strings.HasPrefix(host, "unix://") {
		candidates = append(candidates, strings.TrimPrefix(host, "unix://"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, ".docker", "run", "docker.sock"))
	}
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		candidates = append(candidates, filepath.Join(runtimeDir, "docker.sock"))
	}
	root = filepath.Clean(root)
	for _, candidate := range candidates {
		rel, err := filepath.Rel(root, filepath.Clean(candidate))
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (r *Runner) Start(ctx context.Context, spec harness.ProcessSpec) (harness.Process, error) {
	args, err := BuildRunArgsChecked(spec)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, r.docker(), args...)
	cmd.Env = append(os.Environ(), envPairs(spec.Env)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("docker stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("docker stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("docker stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start docker: %w", err)
	}
	return &process{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr, name: spec.Name, docker: r.docker()}, nil
}

func envPairs(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

type process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.Reader
	stderr io.Reader
	name   string
	docker string

	waitOnce sync.Once
	waitErr  error
	waitDone chan struct{}
	killOnce sync.Once
}

func (p *process) Stdin() io.WriteCloser { return p.stdin }
func (p *process) Stdout() io.Reader     { return p.stdout }
func (p *process) Stderr() io.Reader     { return p.stderr }

func (p *process) Wait(ctx context.Context) (harness.ExitStatus, error) {
	if p.waitDone == nil {
		p.waitDone = make(chan struct{})
	}
	p.waitOnce.Do(func() {
		go func() {
			p.waitErr = p.cmd.Wait()
			close(p.waitDone)
		}()
	})
	select {
	case <-p.waitDone:
		if p.waitErr != nil {
			var exitErr *exec.ExitError
			if errors.As(p.waitErr, &exitErr) {
				return harness.ExitStatus{Code: exitErr.ExitCode()}, p.waitErr
			}
			return harness.ExitStatus{}, p.waitErr
		}
		return harness.ExitStatus{Code: 0}, nil
	case <-ctx.Done():
		_ = p.Kill(context.Background())
		return harness.ExitStatus{}, ctx.Err()
	}
}

func (p *process) Kill(ctx context.Context) error {
	var err error
	p.killOnce.Do(func() {
		if p.cmd != nil && p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		if p.name != "" {
			cmd := exec.CommandContext(ctx, p.docker, "rm", "-f", p.name)
			err = cmd.Run()
		}
	})
	return err
}

// CleanupOrphans removes labeled containers and networks from interrupted runs
// of this deployment. Resources owned by another harness process are left
// alone even when it is still running.
func (r *Runner) CleanupOrphans(ctx context.Context) error {
	filters := []string{"--filter", "label=harness.managed=true"}
	if r != nil && r.Owner != "" {
		filters = append(filters, "--filter", "label=harness.owner="+naming.Identifier(r.Owner, "target"))
	}
	commands := [][]string{
		append([]string{"ps", "-aq"}, filters...),
		append([]string{"network", "ls", "-q"}, filters...),
	}
	var ids []string
	for _, args := range commands[:1] {
		cmd := exec.CommandContext(ctx, r.docker(), args...)
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("list orphan containers: %w", err)
		}
		ids = append(ids, strings.Fields(string(out))...)
	}
	if len(ids) > 0 {
		args := append([]string{"rm", "-f"}, ids...)
		if err := exec.CommandContext(ctx, r.docker(), args...).Run(); err != nil {
			return fmt.Errorf("remove orphan containers: %w", err)
		}
	}
	cmd := exec.CommandContext(ctx, r.docker(), commands[1]...)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("list orphan networks: %w", err)
	}
	networks := strings.Fields(string(out))
	if len(networks) > 0 {
		args := append([]string{"network", "rm"}, networks...)
		if err := exec.CommandContext(ctx, r.docker(), args...).Run(); err != nil {
			return fmt.Errorf("remove orphan networks: %w", err)
		}
	}
	return nil
}

var _ harness.ProcessStarter = (*Runner)(nil)
var _ harness.ResourceCleaner = (*Runner)(nil)
