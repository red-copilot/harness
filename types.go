package harness

import (
	"context"
	"encoding/json"
	"io"
	"time"
)

// Status is the lifecycle state of a run or agent session.
type Status string

const (
	StatusQueued      Status = "queued"
	StatusStarting    Status = "starting"
	StatusRunning     Status = "running"
	StatusVerifying   Status = "verifying"
	StatusSucceeded   Status = "succeeded"
	StatusFailed      Status = "failed"
	StatusCancelled   Status = "cancelled"
	StatusTimedOut    Status = "timed_out"
	StatusInterrupted Status = "interrupted"
)

// Task is the caller-owned objective and its read-only inputs.
type Task struct {
	ID        string            `json:"id" yaml:"id"`
	Objective string            `json:"objective" yaml:"objective"`
	Inputs    []Mount           `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	Knowledge []Mount           `json:"knowledge,omitempty" yaml:"knowledge,omitempty"`
	Skills    []Mount           `json:"skills,omitempty" yaml:"skills,omitempty"`
	Targets   []Target          `json:"targets,omitempty" yaml:"targets,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// Mount describes a host path exposed to an agent container.
type Mount struct {
	Source      string `json:"source" yaml:"source"`
	Destination string `json:"destination" yaml:"destination"`
	ReadOnly    bool   `json:"read_only,omitempty" yaml:"read_only,omitempty"`
}

// Target is a provisioned or externally supplied attack surface.
type Target struct {
	ID        string            `json:"id,omitempty" yaml:"id,omitempty"`
	Addresses []string          `json:"addresses,omitempty" yaml:"addresses,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// Budget applies to one run. Wall time is shared by all sessions; usage
// ceilings are recorded per session and aggregated by the runner.
type Budget struct {
	WallTime      time.Duration `json:"wall_time,omitempty" yaml:"wall_time,omitempty"`
	MaxTokens     int           `json:"max_tokens,omitempty" yaml:"max_tokens,omitempty"`
	MaxModelCalls int           `json:"max_model_calls,omitempty" yaml:"max_model_calls,omitempty"`
	MaxToolCalls  int           `json:"max_tool_calls,omitempty" yaml:"max_tool_calls,omitempty"`
	MaxCostUSD    float64       `json:"max_cost_usd,omitempty" yaml:"max_cost_usd,omitempty"`
}

// Usage is reported by the agent transport. It never proves success.
type Usage struct {
	InputTokens  int     `json:"input_tokens,omitempty"`
	OutputTokens int     `json:"output_tokens,omitempty"`
	TotalTokens  int     `json:"total_tokens,omitempty"`
	ModelCalls   int     `json:"model_calls,omitempty"`
	ToolCalls    int     `json:"tool_calls,omitempty"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
}

// PiSpec configures the built-in Pi RPC session factory.
type PiSpec struct {
	Provider       string   `json:"provider,omitempty" yaml:"provider,omitempty"`
	Model          string   `json:"model,omitempty" yaml:"model,omitempty"`
	Thinking       string   `json:"thinking,omitempty" yaml:"thinking,omitempty"`
	Tools          []string `json:"tools,omitempty" yaml:"tools,omitempty"`
	ExtraArgs      []string `json:"extra_args,omitempty" yaml:"extra_args,omitempty"`
	EnvPassthrough []string `json:"env_passthrough,omitempty" yaml:"env_passthrough,omitempty"`
	SystemPrompt   string   `json:"system_prompt,omitempty" yaml:"system_prompt,omitempty"`
	Skills         []string `json:"skills,omitempty" yaml:"skills,omitempty"`
	Image          string   `json:"image,omitempty" yaml:"image,omitempty"`
	ModelsJSON     string   `json:"models_json,omitempty" yaml:"models_json,omitempty"`
}

// AgentSpec describes one independently runnable agent session.
type AgentSpec struct {
	ID        string            `json:"id" yaml:"id"`
	Image     string            `json:"image,omitempty" yaml:"image,omitempty"`
	Command   []string          `json:"command,omitempty" yaml:"command,omitempty"`
	Env       map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	EnvFrom   []string          `json:"env_from,omitempty" yaml:"env_from,omitempty"`
	Knowledge []Mount           `json:"knowledge,omitempty" yaml:"knowledge,omitempty"`
	Skills    []Mount           `json:"skills,omitempty" yaml:"skills,omitempty"`
	Network   string            `json:"network,omitempty" yaml:"network,omitempty"`
	CapAdd    []string          `json:"cap_add,omitempty" yaml:"cap_add,omitempty"`
	Pi        *PiSpec           `json:"pi,omitempty" yaml:"pi,omitempty"`
	Factory   SessionFactory    `json:"-" yaml:"-"`
}

// ProcessSpec is the runtime contract used by the built-in Docker starter.
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

// ExitStatus is returned when a session process exits.
type ExitStatus struct {
	Code int
}

// Process is a bidirectional stdio process, usually Pi inside Docker.
type Process interface {
	Stdin() io.WriteCloser
	Stdout() io.Reader
	Stderr() io.Reader
	Wait(context.Context) (ExitStatus, error)
	Kill(context.Context) error
}

// ProcessStarter starts a process with the supplied container contract.
type ProcessStarter interface {
	Start(context.Context, ProcessSpec) (Process, error)
}

// AgentEvent is a normalized event emitted by any agent session. A session
// participating in exploration control must emit turn_end after one assistant
// turn and its tool calls complete.
type AgentEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// AgentResult is the final observation of one agent session.
type AgentResult struct {
	Status   Status `json:"status"`
	ExitCode int    `json:"exit_code,omitempty"`
	Usage    Usage  `json:"usage,omitempty"`
	Error    string `json:"error,omitempty"`
}

// LogEvent is a raw agent stdout/stderr line. It is persisted separately from
// normalized protocol events.
type LogEvent struct {
	Stream string
	Text   string
}

// LogSource is optionally implemented by an AgentSession that exposes raw logs.
type LogSource interface {
	Logs() <-chan LogEvent
}

// UsageSource is optionally implemented by a session that can report current
// usage before the session finishes, allowing live budget enforcement.
type UsageSource interface {
	Usage() Usage
}

// AgentSession is the protocol-independent extension point for a custom agent.
type AgentSession interface {
	Prompt(context.Context, string) error
	Steer(context.Context, string) error
	Events() <-chan AgentEvent
	Wait(context.Context) (AgentResult, error)
	Abort(context.Context) error
	Close(context.Context) error
}

// SessionRequest is passed to a SessionFactory. Starter is scoped to the
// caller's run and is the only supported way to create an agent process.
type SessionRequest struct {
	RunID        string
	SessionID    string
	Agent        AgentSpec
	Task         Task
	Target       Target
	WorkspaceDir string
	TaskDir      string
	SkillsDir    string
	Env          map[string]string
	Starter      ProcessStarter
}

// SessionFactory creates an AgentSession, defaulting to Pi.
type SessionFactory interface {
	NewSession(context.Context, SessionRequest) (AgentSession, error)
}

// AgentSessionFactory is the public name intended for callers replacing Pi.
type AgentSessionFactory = SessionFactory

// ProvisionRequest asks a backend to create or discover a target.
type ProvisionRequest struct {
	RunID    string
	Task     Task
	Metadata map[string]string
}

// Completion is the backend's authoritative completion signal.
type Completion struct {
	Done     bool              `json:"done"`
	Success  bool              `json:"success"`
	Score    float64           `json:"score,omitempty"`
	Message  string            `json:"message,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Backend owns target lifecycle. TSec, local Docker, Compose and external
// targets all implement this contract.
type Backend interface {
	Provision(context.Context, ProvisionRequest) (Target, error)
	Observe(context.Context, Target) (Completion, error)
	Close(context.Context, Target) error
}

// SessionResult is the audit record for one agent session.
type SessionResult struct {
	ID       string `json:"id"`
	Status   Status `json:"status"`
	ExitCode int    `json:"exit_code,omitempty"`
	Usage    Usage  `json:"usage,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Verdict is produced only by a verifier or benchmark evaluator.
type Verdict struct {
	Success  bool            `json:"success"`
	Score    float64         `json:"score,omitempty"`
	Message  string          `json:"message,omitempty"`
	Evidence json.RawMessage `json:"evidence,omitempty"`
}

// VerifyRequest gives a verifier read-only run context and the session records.
type VerifyRequest struct {
	RunID         string
	Task          Task
	Target        Target
	Sessions      []SessionResult
	WorkspaceRoot string
	Store         Store
}

// Verifier is the only local path that can declare success.
type Verifier interface {
	Verify(context.Context, VerifyRequest) (Verdict, error)
}

// RunSpec is the Go-first input contract for one run.
type RunSpec struct {
	Task          Task        `json:"task" yaml:"task"`
	Target        Target      `json:"target,omitempty" yaml:"target,omitempty"`
	Agents        []AgentSpec `json:"agents" yaml:"agents"`
	Budget        Budget      `json:"budget,omitempty" yaml:"budget,omitempty"`
	Backend       Backend     `json:"-" yaml:"-"`
	Verifier      Verifier    `json:"-" yaml:"-"`
	SecretEnv     []string    `json:"secret_env,omitempty" yaml:"secret_env,omitempty"`
	WorkspaceRoot string      `json:"workspace_root,omitempty" yaml:"workspace_root,omitempty"`
}

// Event is an append-only run or session event.
type Event struct {
	Seq       int64           `json:"seq,omitempty"`
	RunID     string          `json:"run_id"`
	SessionID string          `json:"session_id,omitempty"`
	Source    string          `json:"source"`
	Type      string          `json:"type"`
	Time      time.Time       `json:"time"`
	Data      json.RawMessage `json:"data,omitempty"`
	Raw       json.RawMessage `json:"raw,omitempty"`
}

// LogRecord is a raw stdout/stderr line, stored separately from protocol events.
type LogRecord struct {
	Seq       int64     `json:"seq,omitempty"`
	RunID     string    `json:"run_id"`
	SessionID string    `json:"session_id,omitempty"`
	Stream    string    `json:"stream"`
	Time      time.Time `json:"time"`
	Text      string    `json:"text"`
}

// RunRecord is the SQLite-backed run row.
type RunRecord struct {
	ID         string          `json:"id"`
	Status     Status          `json:"status"`
	Spec       json.RawMessage `json:"spec,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
	SweepID    string          `json:"sweep_id,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	StartedAt  *time.Time      `json:"started_at,omitempty"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
}

// SweepRecord is the persisted batch state for TSec or other bulk runs.
type SweepRecord struct {
	ID         string          `json:"id"`
	Status     Status          `json:"status"`
	Spec       json.RawMessage `json:"spec,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
}

// VerdictRecord is the persisted authoritative verdict.
type VerdictRecord struct {
	RunID     string          `json:"run_id"`
	Success   bool            `json:"success"`
	Score     float64         `json:"score,omitempty"`
	Message   string          `json:"message,omitempty"`
	Evidence  json.RawMessage `json:"evidence,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// Result is returned by Run.Wait.
type Result struct {
	RunID      string          `json:"run_id"`
	Status     Status          `json:"status"`
	Verdict    *Verdict        `json:"verdict,omitempty"`
	Sessions   []SessionResult `json:"sessions,omitempty"`
	Usage      Usage           `json:"usage,omitempty"`
	Error      string          `json:"error,omitempty"`
	StartedAt  *time.Time      `json:"started_at,omitempty"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
}

// LogPageReader is an optional Store extension for bounded log pagination.
// Stores that do not implement it fall back to ListLogs in the HTTP server.
type LogPageReader interface {
	ListLogsPage(context.Context, string, int64, int, bool) ([]LogRecord, error)
}

// Store is the sole canonical persistence contract.
type Store interface {
	CreateRun(context.Context, RunRecord) error
	UpdateRun(context.Context, RunRecord) error
	GetRun(context.Context, string) (RunRecord, error)
	ListRuns(context.Context, int) ([]RunRecord, error)
	AppendEvent(context.Context, Event) (Event, error)
	ListEvents(context.Context, string, int64) ([]Event, error)
	AppendLog(context.Context, LogRecord) (LogRecord, error)
	ListLogs(context.Context, string, int64) ([]LogRecord, error)
	SaveVerdict(context.Context, VerdictRecord) error
	GetVerdict(context.Context, string) (VerdictRecord, error)
	SaveSweep(context.Context, SweepRecord) error
	GetSweep(context.Context, string) (SweepRecord, error)
	ListSweeps(context.Context, int) ([]SweepRecord, error)
	MarkInterrupted(context.Context) error
	Close() error
}

// FactTrust records how strongly a fact is established by explorer evidence.
// It never represents task success; only a Verifier or backend completion can
// do that.
type FactTrust string

const (
	FactObserved     FactTrust = "observed"
	FactCorroborated FactTrust = "corroborated"
	FactRefuted      FactTrust = "refuted"
)

// Fact is one bounded assertion in the shared exploration ledger.
type Fact struct {
	ID        string    `json:"id"`
	Claim     string    `json:"claim"`
	Evidence  string    `json:"evidence"`
	Source    string    `json:"source"`
	Sources   []string  `json:"sources"`
	Trust     FactTrust `json:"trust"`
	Contested bool      `json:"contested"`
	Targets   []string  `json:"targets,omitempty"`
	RefutedBy []string  `json:"refuted_by,omitempty"`
}

// ExplorerClaim is one claim produced by an explorer report.
type ExplorerClaim struct {
	Claim    string   `json:"claim"`
	Evidence string   `json:"evidence"`
	Targets  []string `json:"targets,omitempty"`
	Supports []string `json:"supports,omitempty"`
	Refutes  []string `json:"refutes,omitempty"`
}

// ExplorerProposal is one bounded next action proposed by a planner or
// executor. The controller validates it before it becomes a FrontierItem.
type ExplorerProposal struct {
	Question         string   `json:"question"`
	Target           string   `json:"target"`
	ExpectedEvidence string   `json:"expected_evidence"`
	Anchors          []string `json:"anchors,omitempty"`
}

// ExplorerReport is the structured result of one frontier execution.
type ExplorerReport struct {
	ItemID   string             `json:"item_id"`
	Verdict  string             `json:"verdict"`
	Claims   []ExplorerClaim    `json:"claims"`
	Next     []ExplorerProposal `json:"next"`
	Commands []string           `json:"commands,omitempty"`
	Errors   []string           `json:"errors,omitempty"`
}
