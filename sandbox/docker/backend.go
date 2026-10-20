package docker

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/red-copilot/harness"
)

// ExternalBackend hands the caller's addresses to the agent. It never creates
// or destroys a target.
type ExternalBackend struct {
	Target harness.Target
}

func (b ExternalBackend) Provision(context.Context, harness.ProvisionRequest) (harness.Target, error) {
	return b.Target, nil
}
func (b ExternalBackend) Observe(context.Context, harness.Target) (harness.Completion, error) {
	return harness.Completion{}, nil
}
func (b ExternalBackend) Close(context.Context, harness.Target) error { return nil }

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
}

func (b ImageBackend) Provision(ctx context.Context, req harness.ProvisionRequest) (harness.Target, error) {
	docker := b.Docker
	if docker == "" {
		docker = "docker"
	}
	if b.Image == "" {
		return harness.Target{}, fmt.Errorf("target image is required")
	}
	name := b.Name
	if name == "" {
		name = "harness_target_" + safeName(req.RunID)
	}
	network := b.Network
	if network == "" {
		network = "harness_net_" + safeName(req.RunID)
		if err := run(ctx, docker, "network", "create",
			"--label", "harness.managed=true", "--label", "harness.run_id="+req.RunID, network); err != nil {
			return harness.Target{}, fmt.Errorf("create target network: %w", err)
		}
	}
	args := []string{"run", "-d", "--name", name, "--network", network,
		"--label", "harness.managed=true", "--label", "harness.run_id=" + req.RunID}
	for _, port := range b.Ports {
		args = append(args, "-p", port)
	}
	for key, value := range b.Env {
		args = append(args, "-e", key+"="+value)
	}
	args = append(args, b.Image)
	args = append(args, b.Command...)
	if err := run(ctx, docker, args...); err != nil {
		return harness.Target{}, fmt.Errorf("start target image: %w", err)
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
	return harness.Target{
		ID: name, Addresses: addresses,
		Metadata: map[string]string{
			"container_name": name, "network": network,
			"target_addresses": strings.Join(addresses, ","),
		},
	}, nil
}

func (b ImageBackend) Observe(context.Context, harness.Target) (harness.Completion, error) {
	return harness.Completion{}, nil
}

func (b ImageBackend) Close(ctx context.Context, target harness.Target) error {
	docker := b.Docker
	if docker == "" {
		docker = "docker"
	}
	if name := target.Metadata["container_name"]; name != "" {
		_ = exec.CommandContext(ctx, docker, "rm", "-f", name).Run()
	}
	if network := target.Metadata["network"]; network != "" && b.Network == "" {
		_ = exec.CommandContext(ctx, docker, "network", "rm", network).Run()
	}
	return nil
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
}

func (b ComposeBackend) Provision(ctx context.Context, req harness.ProvisionRequest) (harness.Target, error) {
	docker := b.Docker
	if docker == "" {
		docker = "docker"
	}
	project := b.ProjectName
	if project == "" {
		project = "harness_" + safeName(req.RunID)
	}
	args := []string{"compose", "-p", project, "-f", filepath.Clean(b.File), "up", "-d"}
	if err := run(ctx, docker, args...); err != nil {
		return harness.Target{}, fmt.Errorf("compose up: %w", err)
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
	return harness.Target{
		ID: project, Addresses: addresses,
		Metadata: map[string]string{
			"compose_project": project, "compose_file": b.File,
			"network": project + "_default",
		},
	}, nil
}

func (b ComposeBackend) Observe(context.Context, harness.Target) (harness.Completion, error) {
	return harness.Completion{}, nil
}

func (b ComposeBackend) Close(ctx context.Context, target harness.Target) error {
	docker := b.Docker
	if docker == "" {
		docker = "docker"
	}
	project := target.Metadata["compose_project"]
	if project == "" {
		return nil
	}
	return run(ctx, docker, "compose", "-p", project, "-f", filepath.Clean(b.File),
		"down", "-v", "--remove-orphans")
}

func run(ctx context.Context, docker string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, docker, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func safeName(value string) string {
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "target"
	}
	return b.String()
}

var _ harness.Backend = ExternalBackend{}
var _ harness.Backend = ImageBackend{}
var _ harness.Backend = ComposeBackend{}
