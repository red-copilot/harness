package harness

import (
	"context"
	"encoding/json"
	"io"
	"time"
)

// AgentRole selects the fixed capability policy enforced by a runtime.
type AgentRole string

const (
	AgentRolePlanner  AgentRole = "planner"
	AgentRoleExecutor AgentRole = "executor"
)

// AgentSpec describes one runtime session class.
type AgentSpec struct {
	ID            string              `json:"id" yaml:"id"`
	Image         string              `json:"image,omitempty" yaml:"image,omitempty"`
	Command       []string            `json:"command,omitempty" yaml:"command,omitempty"`
	Env           map[string]string   `json:"env,omitempty" yaml:"env,omitempty"`
	EnvFrom       []string            `json:"env_from,omitempty" yaml:"env_from,omitempty"`
	Mounts        []Mount             `json:"mounts,omitempty" yaml:"mounts,omitempty"`
	Network       string              `json:"network,omitempty" yaml:"network,omitempty"`
	CapAdd        []string            `json:"cap_add,omitempty" yaml:"cap_add,omitempty"`
	Resources     ResourceSpec        `json:"resources,omitempty" yaml:"resources,omitempty"`
	RuntimeConfig json.RawMessage     `json:"runtime,omitempty" yaml:"runtime,omitempty"`
	Runtime       AgentRuntimeFactory `json:"-" yaml:"-"`
}

// Token usage and turn summaries are produced by the core turn protocol.
type TurnResult struct {
	AssistantText string `json:"assistant_text,omitempty"`
	Usage         Usage  `json:"usage,omitempty"`
}

// TurnLimits are enforced while the agent turn is live.
type TurnLimits struct {
	MaxToolCalls int
	ToolTimeout  time.Duration
}

type SessionResult struct {
	ID       string `json:"id"`
	Status   Status `json:"status"`
	ExitCode int    `json:"exit_code,omitempty"`
	Usage    Usage  `json:"usage,omitempty"`
	Error    string `json:"error,omitempty"`
}

// AgentEventKind is the runtime-neutral signal the turn protocol consumes.
// Adapters translate their own wire records into these signals; the control
// plane never inspects Type or Data, which exist for the audit trail only.
type AgentEventKind string

const (
	AgentEventMessage   AgentEventKind = "message"
	AgentEventToolStart AgentEventKind = "tool_start"
	AgentEventToolEnd   AgentEventKind = "tool_end"
	AgentEventSettled   AgentEventKind = "settled"
	AgentEventEnd       AgentEventKind = "end"
)

// AgentEvent is one runtime event: the adapter's native record for audit
// (`Type`, `Data`) plus the normalized signal the core acts on (`Kind` and its
// detail fields). An empty Kind is audit-only.
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

// RuntimeRequest carries all runtime-neutral session inputs.
type RuntimeRequest struct {
	RunID        string
	SessionID    string
	Role         AgentRole
	Agent        AgentSpec
	Task         Task
	Target       Target
	WorkspaceDir string
	TaskDir      string
	SkillsDir    string
	Env          map[string]string
	Starter      ProcessStarter
	// Redactor is the Run's known-secret redactor. Adapters must apply it
	// before writing any configuration into an agent-visible path.
	Redactor *Redactor
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
