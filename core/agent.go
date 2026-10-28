package core

import (
	"context"
	"encoding/json"
	"io"
	"time"
)

// AgentRole selects the fixed capability policy enforced by a runtime.
type AgentRole string

const (
	AgentRoleReasoner AgentRole = "reasoner"
	AgentRoleExecutor AgentRole = "executor"
)

// AgentDescriptor is the serializable, runtime-independent portion of an agent.
type AgentDescriptor struct {
	ID            string            `json:"id" yaml:"id"`
	Image         string            `json:"image,omitempty" yaml:"image,omitempty"`
	Command       []string          `json:"command,omitempty" yaml:"command,omitempty"`
	Env           map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	EnvFrom       []string          `json:"env_from,omitempty" yaml:"env_from,omitempty"`
	Mounts        []Mount           `json:"mounts,omitempty" yaml:"mounts,omitempty"`
	Network       string            `json:"network,omitempty" yaml:"network,omitempty"`
	CapAdd        []string          `json:"cap_add,omitempty" yaml:"cap_add,omitempty"`
	Resources     ResourceSpec      `json:"resources,omitempty" yaml:"resources,omitempty"`
	RuntimeConfig json.RawMessage   `json:"runtime,omitempty" yaml:"runtime,omitempty"`
}

// TurnResult is the bounded result of one runtime turn.
type TurnResult struct {
	AssistantText string `json:"assistant_text,omitempty"`
	Usage         Usage  `json:"usage,omitempty"`
}

// TurnLimits are enforced while an agent turn is live.
type TurnLimits struct {
	MaxToolCalls      int
	MaxAssistantBytes int
	ToolTimeout       time.Duration
}

// SessionResult is the immutable terminal projection of one runtime session.
type SessionResult struct {
	ID       string `json:"id"`
	Status   Status `json:"status"`
	ExitCode int    `json:"exit_code,omitempty"`
	Usage    Usage  `json:"usage,omitempty"`
	Error    string `json:"error,omitempty"`
}

// AgentEventKind is the runtime-neutral signal the turn protocol consumes.
type AgentEventKind string

const (
	AgentEventMessage   AgentEventKind = "message"
	AgentEventToolStart AgentEventKind = "tool_start"
	AgentEventToolEnd   AgentEventKind = "tool_end"
	AgentEventSettled   AgentEventKind = "settled"
	AgentEventEnd       AgentEventKind = "end"
	AgentEventError     AgentEventKind = "error"
)

// AgentEvent carries the adapter-native audit record and normalized control signal.
type AgentEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`

	Kind  AgentEventKind `json:"-"`
	Text  string         `json:"-"`
	Tools []string       `json:"-"`

	Tool       string `json:"-"`
	ToolPath   string `json:"-"`
	WritesFile bool   `json:"-"`
}

// ProcessSpec is the sandbox contract.
type ProcessSpec struct {
	Image        string
	Command      []string
	Env          map[string]string
	Mounts       []Mount
	Network      string
	CapAdd       []string
	Name         string
	Labels       map[string]string
	WorkingDir   string
	User         string
	ReadOnlyRoot bool
	PidsLimit    int
	Memory       string
	CPUs         string
}

type ExitStatus struct {
	Code int
}

type Process interface {
	Stdin() io.WriteCloser
	Stdout() io.Reader
	Stderr() io.Reader
	Wait(context.Context) (ExitStatus, error)
	Kill(context.Context) error
}
