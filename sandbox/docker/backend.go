package docker

import (
	"context"
	"errors"
	"fmt"
	"github.com/red-copilot/harness/internal/naming"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/red-copilot/harness"
)

// ExternalBackend hands the caller's addresses to the agent. It never creates
// or destroys a target.
type ExternalBackend struct {
	Target  harness.TargetSpec
	Runtime *harness.TargetRuntime
}

func (b ExternalBackend) Provision(_ context.Context, req harness.ProvisionRequest) (harness.TargetBinding, error) {
	target := b.Target
	if target.ID == "" {
		target = req.Target
	}
	return harness.TargetBinding{TargetID: target.ID, Addresses: append([]string(nil), target.Addresses...), Isolation: target.Isolation, Runtime: b.Runtime}, nil
}
func (b ExternalBackend) Close(context.Context, harness.TargetBinding) error { return nil }

// ImageBackend provisions a one-shot Docker target for local offensive work.
type ImageBackend struct {
	Docker    string
	Image     string
	Command   []string
	Env       map[string]string
	Ports     []string
	Network   string
	Addresses []string
	Name      string
	// Owner scopes orphan cleanup to this deployment. Empty means the host
	// name, matching docker.New.
	Owner string
}

func (b ImageBackend) Provision(ctx context.Context, req harness.ProvisionRequest) (harness.TargetBinding, error) {
	docker := b.Docker
	if docker == "" {
		docker = "docker"
	}
	if b.Image == "" {
		return harness.TargetBinding{}, fmt.Errorf("target image is required")
	}
	name := b.Name
	if name == "" {
		name = "harness_target_" + naming.Identifier(req.RunID, "target")
	}
	network := b.Network
	createdNetwork := false
	if network == "" {
		network = "harness_net_" + naming.Identifier(req.RunID, "target")
		if err := run(ctx, docker, "network", "create", "--label", "harness.managed=true", "--label", "harness.run_id="+req.RunID, "--label", "harness.owner="+ownerLabel(b.Owner), network); err != nil {
			return harness.TargetBinding{}, fmt.Errorf("create target network: %w", err)
		}
		createdNetwork = true
	}
	args := []string{"run", "-d", "--name", name, "--network", network,
		"--label", "harness.managed=true", "--label", "harness.run_id=" + req.RunID,
		"--label", "harness.owner=" + ownerLabel(b.Owner)}
	for _, port := range b.Ports {
		args = append(args, "-p", port)
	}
	// Secret values reach the container through the Docker client environment,
	// never argv: argv is world-readable through process listings.
	keys := make([]string, 0, len(b.Env))
	for key := range b.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		args = append(args, "-e", key)
		env = append(env, key+"="+b.Env[key])
	}
	args = append(args, b.Image)
	args = append(args, b.Command...)
	if err := runWithEnv(ctx, docker, env, args...); err != nil {
		_ = run(ctx, docker, "rm", "-f", name)
		if createdNetwork {
			_ = run(ctx, docker, "network", "rm", network)
		}
		return harness.TargetBinding{}, fmt.Errorf("start target image: %w", err)
	}
	addresses := append([]string(nil), b.Addresses...)
	if len(addresses) == 0 && len(b.Ports) > 0 {
		for _, port := range b.Ports {
			parts := strings.SplitN(port, ":", 2)
			if len(parts) != 2 {
				continue
			}
			out, err := exec.CommandContext(ctx, docker, "port", name, parts[1]+"/tcp").Output()
			if err == nil {
				for _, line := range strings.Fields(string(out)) {
					addresses = append(addresses, line)
				}
			}
		}
	}
	return harness.TargetBinding{
		TargetID: req.Target.ID, Addresses: addresses, Isolation: req.Target.Isolation,
		Runtime: &harness.TargetRuntime{ContainerName: name, Network: network},
	}, nil
}

func (b ImageBackend) Close(ctx context.Context, target harness.TargetBinding) error {
	docker := b.Docker
	if docker == "" {
		docker = "docker"
	}
	if target.Runtime == nil {
		return nil
	}
	var cleanupErr error
	if name := target.Runtime.ContainerName; name != "" {
		if err := exec.CommandContext(ctx, docker, "rm", "-f", name).Run(); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove target container %s: %w", name, err))
		}
	}
	if network := target.Runtime.Network; network != "" && b.Network == "" {
		if err := exec.CommandContext(ctx, docker, "network", "rm", network).Run(); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove target network %s: %w", network, err))
		}
	}
	return cleanupErr
}

// ComposeBackend runs a caller-supplied Compose project and tears it down on
// completion. Project-scoped names prevent collisions between runs.
type ComposeBackend struct {
	Docker      string
	File        string
	ProjectName string
	Service     string
	Port        string
	Addresses   []string
	// Owner scopes orphan cleanup to this deployment. Empty means the host
	// name, matching docker.New.
	Owner string
}

func (b ComposeBackend) Provision(ctx context.Context, req harness.ProvisionRequest) (harness.TargetBinding, error) {
	docker := b.Docker
	if docker == "" {
		docker = "docker"
	}
	project := b.ProjectName
	if project == "" {
		project = "harness_" + naming.Identifier(req.RunID, "target")
	}
	composeFile := filepath.Clean(b.File)
	// Compose has no per-invocation label flag, so a generated override adds
	// the harness labels to every service. Without them a crashed project is
	// invisible to orphan cleanup.
	services, err := composeServices(ctx, docker, composeFile)
	if err != nil {
		return harness.TargetBinding{}, err
	}
	files := []string{"-f", composeFile}
	override, err := writeComposeOverride(services, map[string]string{
		"harness.managed": "true",
		"harness.run_id":  req.RunID,
		"harness.owner":   ownerLabel(b.Owner),
	})
	if err != nil {
		return harness.TargetBinding{}, err
	}
	files = append(files, "-f", override)
	args := append([]string{"compose", "-p", project}, append(files, "up", "-d")...)
	if err := run(ctx, docker, args...); err != nil {
		_ = run(ctx, docker, append([]string{"compose", "-p", project}, append(files, "down", "-v", "--remove-orphans")...)...)
		_ = os.Remove(override)
		return harness.TargetBinding{}, fmt.Errorf("compose up: %w", err)
	}
	addresses := append([]string(nil), b.Addresses...)
	if len(addresses) == 0 && b.Service != "" && b.Port != "" {
		out, err := exec.CommandContext(ctx, docker, "compose", "-p", project, "-f", b.File,
			"port", b.Service, b.Port).Output()
		if err == nil {
			for _, line := range strings.Fields(string(out)) {
				addresses = append(addresses, line)
			}
		}
		if len(addresses) == 0 {
			addresses = append(addresses, b.Service+":"+b.Port)
		}
	}
	return harness.TargetBinding{
		TargetID: req.Target.ID, Addresses: addresses, Isolation: req.Target.Isolation,
		Runtime: &harness.TargetRuntime{ComposeProject: project, ComposeFile: composeFile, ComposeOverride: override, Network: project + "_default"},
	}, nil
}

func (b ComposeBackend) Close(ctx context.Context, target harness.TargetBinding) error {
	docker := b.Docker
	if docker == "" {
		docker = "docker"
	}
	if target.Runtime == nil || target.Runtime.ComposeProject == "" {
		return nil
	}
	project := target.Runtime.ComposeProject
	file := target.Runtime.ComposeFile
	if file == "" {
		file = filepath.Clean(b.File)
	}
	files := []string{"-f", file}
	if target.Runtime.ComposeOverride != "" {
		files = append(files, "-f", target.Runtime.ComposeOverride)
		defer os.Remove(target.Runtime.ComposeOverride)
	}
	args := append([]string{"compose", "-p", project}, append(files, "down", "-v", "--remove-orphans")...)
	return run(ctx, docker, args...)
}

func run(ctx context.Context, docker string, args ...string) error {
	return runWithEnv(ctx, docker, nil, args...)
}

// runWithEnv runs the docker client with extra environment entries. Secret
// values travel here so they never appear in the process argument list.
func runWithEnv(ctx context.Context, docker string, extraEnv []string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, docker, args...)
	if len(extraEnv) > 0 {
		// Drop inherited duplicates first so the value we supply is the one the
		// docker client resolves for `-e NAME`.
		names := map[string]bool{}
		for _, entry := range extraEnv {
			if index := strings.IndexByte(entry, '='); index > 0 {
				names[entry[:index]] = true
			}
		}
		inherited := make([]string, 0, len(os.Environ()))
		for _, entry := range os.Environ() {
			if index := strings.IndexByte(entry, '='); index > 0 && names[entry[:index]] {
				continue
			}
			inherited = append(inherited, entry)
		}
		cmd.Env = append(inherited, extraEnv...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// composeServices lists the services a compose file declares, so the label
// override covers every one of them.
func composeServices(ctx context.Context, docker, file string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, docker, "compose", "-f", file, "config", "--services").Output()
	if err != nil {
		return nil, fmt.Errorf("list compose services: %w", err)
	}
	return strings.Fields(string(out)), nil
}

// writeComposeOverride writes a temporary compose file that adds labels to
// each service. The caller removes it after the project is torn down.
func writeComposeOverride(services []string, labels map[string]string) (string, error) {
	if len(services) == 0 {
		return "", nil
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var body strings.Builder
	body.WriteString("services:\n")
	for _, service := range services {
		fmt.Fprintf(&body, "  %s:\n    labels:\n", strconv.Quote(service))
		for _, key := range keys {
			fmt.Fprintf(&body, "      %s: %s\n", strconv.Quote(key), strconv.Quote(labels[key]))
		}
	}
	file, err := os.CreateTemp("", "harness-compose-*.yaml")
	if err != nil {
		return "", fmt.Errorf("write compose override: %w", err)
	}
	if _, err := file.WriteString(body.String()); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return "", fmt.Errorf("write compose override: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return "", fmt.Errorf("write compose override: %w", err)
	}
	return file.Name(), nil
}

// ownerLabel identifies the harness deployment that owns a Docker resource.
// A cleaner and a backend constructed separately must agree on it, so both
// default to the host name.
func ownerLabel(owner string) string {
	if owner == "" {
		return defaultOwner()
	}
	return naming.Identifier(owner, "target")
}

func defaultOwner() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "harness"
	}
	return naming.Identifier(host, "target")
}

// These adapters own a Target's lifecycle only. Only TSec or an external
// CompletionSource is an authority for the security outcome.
var _ harness.TargetProvisioner = ExternalBackend{}
var _ harness.TargetProvisioner = ImageBackend{}
var _ harness.TargetProvisioner = ComposeBackend{}
