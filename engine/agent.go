package engine

import (
	"encoding/json"

	"github.com/red-copilot/harness/core"
)

// AgentSpec binds a serializable descriptor to one runtime factory.
type AgentSpec struct {
	ID            string              `json:"id" yaml:"id"`
	Image         string              `json:"image,omitempty" yaml:"image,omitempty"`
	Command       []string            `json:"command,omitempty" yaml:"command,omitempty"`
	Env           map[string]string   `json:"env,omitempty" yaml:"env,omitempty"`
	EnvFrom       []string            `json:"env_from,omitempty" yaml:"env_from,omitempty"`
	Mounts        []core.Mount        `json:"mounts,omitempty" yaml:"mounts,omitempty"`
	Network       string              `json:"network,omitempty" yaml:"network,omitempty"`
	CapAdd        []string            `json:"cap_add,omitempty" yaml:"cap_add,omitempty"`
	Resources     core.ResourceSpec   `json:"resources,omitempty" yaml:"resources,omitempty"`
	RuntimeConfig json.RawMessage     `json:"runtime,omitempty" yaml:"runtime,omitempty"`
	Runtime       AgentRuntimeFactory `json:"-" yaml:"-"`
}

// Descriptor returns the runtime-independent public shape.
func (s AgentSpec) Descriptor() core.AgentDescriptor {
	return core.AgentDescriptor{
		ID: s.ID, Image: s.Image, Command: append([]string(nil), s.Command...),
		Env: cloneStrings(s.Env), EnvFrom: append([]string(nil), s.EnvFrom...),
		Mounts: append([]core.Mount(nil), s.Mounts...), Network: s.Network,
		CapAdd: append([]string(nil), s.CapAdd...), Resources: s.Resources,
		RuntimeConfig: append(json.RawMessage(nil), s.RuntimeConfig...),
	}
}

// AgentFromDescriptor binds one descriptor to a runtime.
func AgentFromDescriptor(descriptor core.AgentDescriptor, runtime AgentRuntimeFactory) AgentSpec {
	return AgentSpec{
		ID: descriptor.ID, Image: descriptor.Image, Command: append([]string(nil), descriptor.Command...),
		Env: cloneStrings(descriptor.Env), EnvFrom: append([]string(nil), descriptor.EnvFrom...),
		Mounts: append([]core.Mount(nil), descriptor.Mounts...), Network: descriptor.Network,
		CapAdd: append([]string(nil), descriptor.CapAdd...), Resources: descriptor.Resources,
		RuntimeConfig: append(json.RawMessage(nil), descriptor.RuntimeConfig...), Runtime: runtime,
	}
}

// RuntimeRequest carries all runtime-neutral session inputs.
type RuntimeRequest struct {
	RunID        string
	SessionID    string
	Role         core.AgentRole
	Agent        AgentSpec
	Task         core.Task
	Target       core.TargetBinding
	WorkspaceDir string
	TaskDir      string
	SkillsDir    string
	Env          map[string]string
	Starter      ProcessStarter
	Redactor     *core.Redactor
}

func cloneStrings(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
