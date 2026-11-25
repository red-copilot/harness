package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/pi"
	"github.com/red-copilot/harness/sandbox/docker"
)

// targetBackend builds the provisioner for one target kind. A nil provisioner
// means the caller manages the target (the "external" kind).
type targetBackend func(TargetFileConfig) (harness.TargetProvisioner, error)

// agentRuntimeFactory builds one agent runtime from its opaque config.
type agentRuntimeFactory func(json.RawMessage, pi.Starter) (harness.AgentRuntimeFactory, error)

// targetBackends is the kind registry for target provisioners. Adding a backend
// is a registration, not an edit to the composition root.
var targetBackends = map[string]targetBackend{
	"":         func(TargetFileConfig) (harness.TargetProvisioner, error) { return nil, nil },
	"external": func(TargetFileConfig) (harness.TargetProvisioner, error) { return nil, nil },
	"image": func(c TargetFileConfig) (harness.TargetProvisioner, error) {
		return docker.ImageBackend{Image: c.Image, Command: c.Command, Ports: c.Ports, Network: c.Network, Addresses: c.Addresses}, nil
	},
	"compose": func(c TargetFileConfig) (harness.TargetProvisioner, error) {
		return docker.ComposeBackend{File: c.ComposeFile, ProjectName: c.ProjectName, Service: c.Service, Port: c.Port, Addresses: c.Addresses}, nil
	},
}

// agentRuntimes is the runtime-type registry. Pi is the default because it is
// the only runtime shipped in this repository.
var agentRuntimes = map[string]agentRuntimeFactory{
	"": func(raw json.RawMessage, starter pi.Starter) (harness.AgentRuntimeFactory, error) {
		return piFactory(raw, starter)
	},
	"pi": func(raw json.RawMessage, starter pi.Starter) (harness.AgentRuntimeFactory, error) {
		return piFactory(raw, starter)
	},
}

func piFactory(raw json.RawMessage, starter pi.Starter) (harness.AgentRuntimeFactory, error) {
	config := pi.Config{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &config); err != nil {
			return nil, fmt.Errorf("invalid pi runtime config: %w", err)
		}
	}
	return pi.NewFactory(config).WithStarter(starter), nil
}

// buildTarget resolves the configured target kind to a provisioner. Unknown
// kinds fail closed and list the registered kinds.
func buildTarget(config TargetFileConfig) (harness.TargetProvisioner, error) {
	backend, ok := targetBackends[strings.ToLower(config.Kind)]
	if !ok {
		return nil, fmt.Errorf("unsupported target kind %q (registered: %s)", config.Kind, registeredTargetKinds())
	}
	return backend(config)
}

// buildRuntime resolves an agent runtime from its declared type. The type is
// read from the runtime config when it carries a "type" key; otherwise Pi is
// assumed, preserving the existing single-runtime configuration.
func buildRuntime(raw json.RawMessage, starter pi.Starter) (harness.AgentRuntimeFactory, error) {
	kind := ""
	if len(raw) > 0 {
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &header); err == nil {
			kind = header.Type
		}
	}
	factory, ok := agentRuntimes[strings.ToLower(kind)]
	if !ok {
		return nil, fmt.Errorf("unsupported agent runtime %q (registered: %s)", kind, registeredRuntimeKinds())
	}
	return factory(raw, starter)
}

func registeredTargetKinds() string {
	kinds := make([]string, 0, len(targetBackends))
	for kind := range targetBackends {
		if kind == "" {
			continue
		}
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return strings.Join(kinds, ", ")
}

func registeredRuntimeKinds() string {
	kinds := make([]string, 0, len(agentRuntimes))
	for kind := range agentRuntimes {
		if kind == "" {
			continue
		}
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return strings.Join(kinds, ", ")
}
