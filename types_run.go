package harness

import (
	"encoding/json"
	"fmt"
	"time"
)

// Status is the lifecycle state of a run.
type Status string

const (
	StatusQueued       Status = "queued"
	StatusProvisioning Status = "provisioning"
	StatusRunning      Status = "running"
	StatusFinalizing   Status = "finalizing"
	StatusVerifying    Status = "verifying"
	StatusSucceeded    Status = "succeeded"
	StatusFailed       Status = "failed"
	StatusCancelled    Status = "cancelled"
	StatusTimedOut     Status = "timed_out"
	StatusInterrupted  Status = "interrupted"
	StatusStagnated    Status = "stagnated"
)

// Budget applies to one Run.
type Budget struct {
	WallTime      time.Duration `json:"wall_time,omitempty" yaml:"wall_time,omitempty"`
	MaxTokens     int           `json:"max_tokens,omitempty" yaml:"max_tokens,omitempty"`
	MaxModelCalls int           `json:"max_model_calls,omitempty" yaml:"max_model_calls,omitempty"`
	MaxToolCalls  int           `json:"max_tool_calls,omitempty" yaml:"max_tool_calls,omitempty"`
	MaxCostUSD    float64       `json:"max_cost_usd,omitempty" yaml:"max_cost_usd,omitempty"`
}

// Usage is transport-reported resource use. It never establishes success.
type Usage struct {
	InputTokens  int     `json:"input_tokens,omitempty"`
	OutputTokens int     `json:"output_tokens,omitempty"`
	TotalTokens  int     `json:"total_tokens,omitempty"`
	ModelCalls   int     `json:"model_calls,omitempty"`
	ToolCalls    int     `json:"tool_calls,omitempty"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
}

// ResourceSpec is the portable sandbox resource contract.
type ResourceSpec struct {
	CPUs      string `json:"cpus,omitempty" yaml:"cpus,omitempty"`
	Memory    string `json:"memory,omitempty" yaml:"memory,omitempty"`
	PidsLimit int    `json:"pids_limit,omitempty" yaml:"pids_limit,omitempty"`
}

// Completion is an authoritative progress signal returned by a Backend.
type Completion struct {
	Done     bool              `json:"done,omitempty"`
	Success  bool              `json:"success,omitempty"`
	Progress int               `json:"progress,omitempty"`
	Total    int               `json:"total,omitempty"`
	Score    float64           `json:"score,omitempty"`
	Message  string            `json:"message,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Config configures a Runner. Starter is required: session runtimes receive it
// through RuntimeRequest and the sandboxed Pi runtime fails closed without it.
type Config struct {
	Store         Store
	Starter       ProcessStarter
	WorkspaceRoot string
	MaxSessions   int
	PollInterval  time.Duration
	CloseTimeout  time.Duration
	Cleaner       ResourceCleaner
	SkipRecovery  bool
}

// Result is the immutable snapshot returned after a Run terminates.
type Result struct {
	RunID        string          `json:"run_id"`
	Status       Status          `json:"status"`
	Verdict      *Verdict        `json:"verdict,omitempty"`
	Branches     []Branch        `json:"branches,omitempty"`
	Attempts     []Attempt       `json:"attempts,omitempty"`
	Observations []Observation   `json:"observations,omitempty"`
	Knowledge    []Knowledge     `json:"knowledge,omitempty"`
	Artifacts    []Artifact      `json:"artifacts,omitempty"`
	Sessions     []SessionResult `json:"sessions,omitempty"`
	Usage        Usage           `json:"usage,omitempty"`
	Error        string          `json:"error,omitempty"`
	StartedAt    *time.Time      `json:"started_at,omitempty"`
	FinishedAt   *time.Time      `json:"finished_at,omitempty"`
}

// RunSpec binds a serializable descriptor to runtime adapter instances.
type RunSpec struct {
	Task      Task
	Target    Target
	Planner   AgentSpec
	Executors []AgentSpec
	Policy    ExplorationPolicy
	Budget    Budget
	// Provisioner owns the Target lifecycle. It is optional: a caller-managed
	// external Target needs no provisioning.
	Provisioner TargetProvisioner
	// Backend is the optional authoritative external completion source.
	Backend          CompletionSource
	Verifier         Verifier
	Artifacts        ArtifactStore
	SecretEnv        []string
	WorkspaceRoot    string
	PlannerStrategy  PlannerStrategy
	ExecutorStrategy ExecutorStrategy
}

// CurrentSchemaVersion is the only descriptor schema this build accepts.
const CurrentSchemaVersion = 1

// ValidateDescriptor rejects descriptors from unknown schema generations.
func ValidateDescriptor(descriptor RunDescriptor) error {
	if descriptor.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("unsupported descriptor schema_version %d (want %d)", descriptor.SchemaVersion, CurrentSchemaVersion)
	}
	return nil
}

// RunDescriptor is the serializable portion of a Run.
type RunDescriptor struct {
	SchemaVersion int               `json:"schema_version" yaml:"schema_version"`
	Task          Task              `json:"task" yaml:"task"`
	Target        Target            `json:"target" yaml:"target"`
	Planner       AgentSpec         `json:"planner" yaml:"planner"`
	Executors     []AgentSpec       `json:"executors" yaml:"executors"`
	Policy        ExplorationPolicy `json:"policy" yaml:"policy"`
	Budget        Budget            `json:"budget" yaml:"budget"`
}

// Event and LogRecord are canonical append-only audit records.
type Event struct {
	Seq           int64           `json:"seq"`
	SchemaVersion int             `json:"schema_version"`
	RunID         string          `json:"run_id"`
	SessionID     string          `json:"session_id,omitempty"`
	Source        string          `json:"source"`
	Type          string          `json:"type"`
	Time          time.Time       `json:"time"`
	Data          json.RawMessage `json:"data,omitempty"`
	Raw           json.RawMessage `json:"raw,omitempty"`
}

type LogRecord struct {
	Seq       int64     `json:"seq"`
	RunID     string    `json:"run_id"`
	SessionID string    `json:"session_id,omitempty"`
	Stream    string    `json:"stream"`
	Time      time.Time `json:"time"`
	Text      string    `json:"text"`
}

type RunRecord struct {
	ID         string          `json:"id"`
	Status     Status          `json:"status"`
	Spec       json.RawMessage `json:"spec,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	StartedAt  *time.Time      `json:"started_at,omitempty"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
}

type VerdictRecord struct {
	RunID     string          `json:"run_id"`
	Success   bool            `json:"success"`
	Score     float64         `json:"score,omitempty"`
	Message   string          `json:"message,omitempty"`
	Evidence  json.RawMessage `json:"evidence,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type LogEvent struct {
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

type ProvisionRequest struct {
	RunID    string
	Task     Task
	Target   Target
	Metadata map[string]string
}

type VerifyRequest struct {
	RunID         string
	Task          Task
	Target        Target
	Sessions      []SessionResult
	WorkspaceRoot string
}
