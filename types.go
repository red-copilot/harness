package harness

import (
	"context"
	"encoding/json"
	"io"
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

// Config configures a Runner.
type Config struct {
	Store         Store
	WorkspaceRoot string
	MaxSessions   int
	PollInterval  time.Duration
	CloseTimeout  time.Duration
	Logger        Logger
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

// Mount describes a host path exposed to an agent container.
type Mount struct {
	Source      string `json:"source" yaml:"source"`
	Destination string `json:"destination" yaml:"destination"`
	ReadOnly    bool   `json:"read_only,omitempty" yaml:"read_only,omitempty"`
}

// Task is the caller-owned objective and read-only inputs.
type Task struct {
	ID        string            `json:"id" yaml:"id"`
	Objective string            `json:"objective" yaml:"objective"`
	Inputs    []Mount           `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	Skills    []Mount           `json:"skills,omitempty" yaml:"skills,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// IsolationMode records who owns the network boundary. v1 supports only
// caller-managed isolation.
type IsolationMode string

const IsolationCallerManaged IsolationMode = "caller_managed"

// Target is one logical authorized attack surface.
type Target struct {
	ID        string            `json:"id" yaml:"id"`
	Addresses []string          `json:"addresses,omitempty" yaml:"addresses,omitempty"`
	Isolation IsolationMode     `json:"isolation" yaml:"isolation"`
	Metadata  map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

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
	Skills        []Mount             `json:"skills,omitempty" yaml:"skills,omitempty"`
	Network       string              `json:"network,omitempty" yaml:"network,omitempty"`
	CapAdd        []string            `json:"cap_add,omitempty" yaml:"cap_add,omitempty"`
	Resources     ResourceSpec        `json:"resources,omitempty" yaml:"resources,omitempty"`
	RuntimeConfig json.RawMessage     `json:"runtime,omitempty" yaml:"runtime,omitempty"`
	Runtime       AgentRuntimeFactory `json:"-" yaml:"-"`
}

// ExplorationPolicy contains all mechanically enforced exploration limits.
type ExplorationPolicy struct {
	MaxDepth                   int           `json:"max_depth" yaml:"max_depth"`
	MaxNodes                   int           `json:"max_nodes" yaml:"max_nodes"`
	MaxChildrenPerBranch       int           `json:"max_children_per_branch" yaml:"max_children_per_branch"`
	MaxActiveBranches          int           `json:"max_active_branches" yaml:"max_active_branches"`
	MaxAttemptsPerBranch       int           `json:"max_attempts_per_branch" yaml:"max_attempts_per_branch"`
	MaxToolCallsPerAttempt     int           `json:"max_tool_calls_per_attempt" yaml:"max_tool_calls_per_attempt"`
	MaxWallTimePerAttempt      time.Duration `json:"max_wall_time_per_attempt" yaml:"max_wall_time_per_attempt"`
	StagnationRounds           int           `json:"stagnation_rounds" yaml:"stagnation_rounds"`
	MaxPlannerTools            int           `json:"max_planner_tools" yaml:"max_planner_tools"`
	MaxPlannerWallTime         time.Duration `json:"max_planner_wall_time" yaml:"max_planner_wall_time"`
	MaxKnowledgeItems          int           `json:"max_knowledge_items" yaml:"max_knowledge_items"`
	MaxKnowledgeStatementBytes int           `json:"max_knowledge_statement_bytes" yaml:"max_knowledge_statement_bytes"`
	MaxKnowledgeDigestItems    int           `json:"max_knowledge_digest_items" yaml:"max_knowledge_digest_items"`
	MaxKnowledgeDigestBytes    int           `json:"max_knowledge_digest_bytes" yaml:"max_knowledge_digest_bytes"`
	MaxReportBytes             int64         `json:"max_report_bytes" yaml:"max_report_bytes"`
	MaxEvidenceFilesPerAttempt int           `json:"max_evidence_files_per_attempt" yaml:"max_evidence_files_per_attempt"`
	MaxArtifactBytes           int64         `json:"max_artifact_bytes" yaml:"max_artifact_bytes"`
	MaxAttemptArtifactBytes    int64         `json:"max_attempt_artifact_bytes" yaml:"max_attempt_artifact_bytes"`
}

// DefaultExplorationPolicy returns the v1 defaults.
func DefaultExplorationPolicy() ExplorationPolicy {
	return ExplorationPolicy{
		MaxDepth: 4, MaxNodes: 15, MaxChildrenPerBranch: 2, MaxActiveBranches: 2,
		MaxAttemptsPerBranch: 3, MaxToolCallsPerAttempt: 20, MaxWallTimePerAttempt: 10 * time.Minute,
		StagnationRounds: 3, MaxPlannerTools: 4, MaxPlannerWallTime: 2 * time.Minute,
		MaxKnowledgeItems: 200, MaxKnowledgeStatementBytes: 200, MaxKnowledgeDigestItems: 8,
		MaxKnowledgeDigestBytes: 2048, MaxReportBytes: 1 << 20, MaxEvidenceFilesPerAttempt: 16,
		MaxArtifactBytes: 10 << 20, MaxAttemptArtifactBytes: 50 << 20,
	}
}

// NormalizeExplorationPolicy fills zero values with defaults while preserving
// explicitly configured positive values.
func NormalizeExplorationPolicy(policy ExplorationPolicy) ExplorationPolicy {
	defaults := DefaultExplorationPolicy()
	if policy.MaxDepth <= 0 {
		policy.MaxDepth = defaults.MaxDepth
	}
	if policy.MaxNodes <= 0 {
		policy.MaxNodes = defaults.MaxNodes
	}
	if policy.MaxChildrenPerBranch <= 0 {
		policy.MaxChildrenPerBranch = defaults.MaxChildrenPerBranch
	}
	if policy.MaxActiveBranches <= 0 {
		policy.MaxActiveBranches = defaults.MaxActiveBranches
	}
	if policy.MaxAttemptsPerBranch <= 0 {
		policy.MaxAttemptsPerBranch = defaults.MaxAttemptsPerBranch
	}
	if policy.MaxToolCallsPerAttempt <= 0 {
		policy.MaxToolCallsPerAttempt = defaults.MaxToolCallsPerAttempt
	}
	if policy.MaxWallTimePerAttempt <= 0 {
		policy.MaxWallTimePerAttempt = defaults.MaxWallTimePerAttempt
	}
	if policy.StagnationRounds <= 0 {
		policy.StagnationRounds = defaults.StagnationRounds
	}
	if policy.MaxPlannerTools <= 0 {
		policy.MaxPlannerTools = defaults.MaxPlannerTools
	}
	if policy.MaxPlannerWallTime <= 0 {
		policy.MaxPlannerWallTime = defaults.MaxPlannerWallTime
	}
	if policy.MaxKnowledgeItems <= 0 {
		policy.MaxKnowledgeItems = defaults.MaxKnowledgeItems
	}
	if policy.MaxKnowledgeStatementBytes <= 0 {
		policy.MaxKnowledgeStatementBytes = defaults.MaxKnowledgeStatementBytes
	}
	if policy.MaxKnowledgeDigestItems <= 0 {
		policy.MaxKnowledgeDigestItems = defaults.MaxKnowledgeDigestItems
	}
	if policy.MaxKnowledgeDigestBytes <= 0 {
		policy.MaxKnowledgeDigestBytes = defaults.MaxKnowledgeDigestBytes
	}
	if policy.MaxReportBytes <= 0 {
		policy.MaxReportBytes = defaults.MaxReportBytes
	}
	if policy.MaxEvidenceFilesPerAttempt <= 0 {
		policy.MaxEvidenceFilesPerAttempt = defaults.MaxEvidenceFilesPerAttempt
	}
	if policy.MaxArtifactBytes <= 0 {
		policy.MaxArtifactBytes = defaults.MaxArtifactBytes
	}
	if policy.MaxAttemptArtifactBytes <= 0 {
		policy.MaxAttemptArtifactBytes = defaults.MaxAttemptArtifactBytes
	}
	return policy
}

// RunSpec binds a serializable descriptor to runtime adapter instances.
type RunSpec struct {
	Task             Task
	Target           Target
	Planner          AgentSpec
	Executors        []AgentSpec
	Policy           ExplorationPolicy
	Budget           Budget
	Backend          Backend
	Verifier         Verifier
	Artifacts        ArtifactStore
	SecretEnv        []string
	WorkspaceRoot    string
	PlannerStrategy  PlannerStrategy
	ExecutorStrategy ExecutorStrategy
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

// Token usage and turn summaries are produced by the core turn protocol.
type TurnResult struct {
	AssistantText string   `json:"assistant_text,omitempty"`
	ToolCalls     []string `json:"tool_calls,omitempty"`
	Usage         Usage    `json:"usage,omitempty"`
}

// TurnLimits are enforced while the agent turn is live.
type TurnLimits struct {
	MaxToolCalls int
	ToolTimeout  time.Duration
}

// AttemptBudget bounds one attempt of a branch.
type AttemptBudget struct {
	MaxAttempts  int           `json:"max_attempts"`
	MaxToolCalls int           `json:"max_tool_calls"`
	MaxWallTime  time.Duration `json:"max_wall_time"`
}

// BranchKind is the semantic role of a tree node.
type BranchKind string

const (
	BranchRoot       BranchKind = "root"
	BranchHypothesis BranchKind = "hypothesis"
	BranchReview     BranchKind = "review"
)

// BranchStatus is the lifecycle state of one branch.
type BranchStatus string

const (
	BranchOpen      BranchStatus = "open"
	BranchActive    BranchStatus = "active"
	BranchPaused    BranchStatus = "paused"
	BranchResolved  BranchStatus = "resolved"
	BranchRefuted   BranchStatus = "refuted"
	BranchBlocked   BranchStatus = "blocked"
	BranchAbandoned BranchStatus = "abandoned"
)

// Branch is one node in the fixed exploration tree.
type Branch struct {
	ID               string       `json:"id"`
	ParentID         string       `json:"parent_id,omitempty"`
	RunID            string       `json:"run_id"`
	TaskID           string       `json:"task_id"`
	Kind             BranchKind   `json:"kind"`
	Depth            int          `json:"depth"`
	Topic            string       `json:"topic"`
	Goal             string       `json:"goal"`
	Hypothesis       string       `json:"hypothesis"`
	SuccessCondition string       `json:"success_condition"`
	FailureCondition string       `json:"failure_condition"`
	Scope            string       `json:"scope"`
	ReviewOf         []string     `json:"review_of,omitempty"`
	Status           BranchStatus `json:"status"`
	Priority         int          `json:"priority,omitempty"`
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
}

type AttemptStatus string

const (
	AttemptRunning   AttemptStatus = "running"
	AttemptCompleted AttemptStatus = "completed"
	AttemptTruncated AttemptStatus = "truncated"
	AttemptFailed    AttemptStatus = "failed"
	AttemptCancelled AttemptStatus = "cancelled"
)

type AttemptOutcome string

const (
	AttemptLocalSuccess AttemptOutcome = "local_success"
	AttemptRefuted      AttemptOutcome = "refuted"
	AttemptBlocked      AttemptOutcome = "blocked"
	AttemptInconclusive AttemptOutcome = "inconclusive"
	AttemptFailure      AttemptOutcome = "failure"
)

// BranchProposal is model-provided semantic input. Identity and limits are
// assigned by the engine.
type BranchProposal struct {
	ParentID         string `json:"parent_id"`
	Topic            string `json:"topic"`
	Goal             string `json:"goal"`
	Hypothesis       string `json:"hypothesis"`
	SuccessCondition string `json:"success_condition"`
	FailureCondition string `json:"failure_condition"`
	Target           string `json:"target"`
	Priority         int    `json:"priority,omitempty"`
}

// ObservationProposal references workspace files, never artifact identities.
type ObservationProposal struct {
	LocalID       string   `json:"local_id"`
	Kind          string   `json:"kind"`
	Summary       string   `json:"summary"`
	Target        string   `json:"target"`
	EvidenceFiles []string `json:"evidence_files"`
}

// Observation is immutable evidence-backed state.
type Observation struct {
	ID        string     `json:"id"`
	RunID     string     `json:"run_id"`
	BranchID  string     `json:"branch_id"`
	AttemptID string     `json:"attempt_id"`
	Kind      string     `json:"kind"`
	Summary   string     `json:"summary"`
	Target    string     `json:"target"`
	Evidence  []Artifact `json:"evidence,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// Attempt is one bounded execution of a Branch.
type Attempt struct {
	ID             string         `json:"id"`
	RunID          string         `json:"run_id"`
	BranchID       string         `json:"branch_id"`
	SessionID      string         `json:"session_id"`
	Status         AttemptStatus  `json:"status"`
	Outcome        AttemptOutcome `json:"outcome"`
	ObservationIDs []string       `json:"observation_ids,omitempty"`
	StartedAt      time.Time      `json:"started_at"`
	FinishedAt     time.Time      `json:"finished_at"`
	Error          string         `json:"error,omitempty"`
}

// AttemptReport is the sole structured executor result.
type AttemptReport struct {
	BranchID      string                `json:"branch_id"`
	Status        AttemptStatus         `json:"status"`
	Outcome       AttemptOutcome        `json:"outcome"`
	Observations  []ObservationProposal `json:"observations,omitempty"`
	Knowledge     []KnowledgeProposal   `json:"knowledge,omitempty"`
	ChildBranches []BranchProposal      `json:"child_branches,omitempty"`
	Error         string                `json:"error,omitempty"`
}

// BranchSummary is a bounded branch view supplied to the planner.
type BranchSummary struct {
	ID         string       `json:"id"`
	ParentID   string       `json:"parent_id,omitempty"`
	Kind       BranchKind   `json:"kind"`
	Depth      int          `json:"depth"`
	Topic      string       `json:"topic"`
	Hypothesis string       `json:"hypothesis"`
	Scope      string       `json:"scope"`
	Status     BranchStatus `json:"status"`
	Priority   int          `json:"priority,omitempty"`
	ReviewOf   []string     `json:"review_of,omitempty"`
}

// AttemptSummary is a bounded attempt view supplied to the planner.
type AttemptSummary struct {
	ID       string         `json:"id"`
	BranchID string         `json:"branch_id"`
	Status   AttemptStatus  `json:"status"`
	Outcome  AttemptOutcome `json:"outcome"`
	Error    string         `json:"error,omitempty"`
}

// PlanRequest and AttemptRequest are deterministic bounded contexts.
type PlanRequest struct {
	Task        Task             `json:"task"`
	Target      Target           `json:"target"`
	Branches    []BranchSummary  `json:"branches"`
	Knowledge   []Knowledge      `json:"knowledge"`
	Progress    Completion       `json:"progress"`
	LastReports []AttemptSummary `json:"last_reports,omitempty"`
}

type PlanProposal struct {
	CreateBranches []BranchProposal `json:"create_branches"`
	CloseBranches  []string         `json:"close_branches,omitempty"`
	Priorities     map[string]int   `json:"priorities,omitempty"`
}

type AttemptRequest struct {
	Task         Task          `json:"task"`
	Target       Target        `json:"target"`
	Branch       Branch        `json:"branch"`
	Knowledge    []Knowledge   `json:"knowledge"`
	Budget       AttemptBudget `json:"budget"`
	WorkspaceDir string        `json:"workspace_dir"`
}

// KnowledgeKind is intentionally adapter-defined.
type KnowledgeKind string

type KnowledgeScope string

const (
	KnowledgeScopeTask     KnowledgeScope = "task"
	KnowledgeScopeTarget   KnowledgeScope = "target"
	KnowledgeScopeInstance KnowledgeScope = "instance"
)

type KnowledgeTrust string

const (
	KnowledgeObserved     KnowledgeTrust = "observed"
	KnowledgeCorroborated KnowledgeTrust = "corroborated"
	KnowledgeContested    KnowledgeTrust = "contested"
	KnowledgeRefuted      KnowledgeTrust = "refuted"
)

type ReusePolicy string

const (
	ReuseStable   ReusePolicy = "stable"
	ReuseInstance ReusePolicy = "instance"
	ReuseNever    ReusePolicy = "never"
)

type KnowledgeProposal struct {
	Topic           string         `json:"topic"`
	Kind            KnowledgeKind  `json:"kind"`
	Statement       string         `json:"statement"`
	Scope           KnowledgeScope `json:"scope"`
	Target          string         `json:"target,omitempty"`
	ObservationRefs []string       `json:"observation_refs"`
	Supports        []string       `json:"supports,omitempty"`
	Refutes         []string       `json:"refutes,omitempty"`
	Reuse           ReusePolicy    `json:"reuse"`
	ExpiresAt       *time.Time     `json:"expires_at,omitempty"`
}

type KnowledgeSource struct {
	RunID       string `json:"run_id"`
	AttemptID   string `json:"attempt_id"`
	SessionID   string `json:"session_id"`
	Observation string `json:"observation"`
}

type Knowledge struct {
	ID            string            `json:"id"`
	TaskID        string            `json:"task_id"`
	RunID         string            `json:"run_id"`
	Topic         string            `json:"topic"`
	Kind          KnowledgeKind     `json:"kind"`
	Statement     string            `json:"statement"`
	Scope         KnowledgeScope    `json:"scope"`
	Target        string            `json:"target,omitempty"`
	Trust         KnowledgeTrust    `json:"trust"`
	Reuse         ReusePolicy       `json:"reuse"`
	Sources       []KnowledgeSource `json:"sources,omitempty"`
	Contradicts   []string          `json:"contradicts,omitempty"`
	ValidFrom     time.Time         `json:"valid_from"`
	LastValidated time.Time         `json:"last_validated"`
	ExpiresAt     *time.Time        `json:"expires_at,omitempty"`
	Version       int64             `json:"version"`
}

type KnowledgeQuery struct {
	TaskID            string
	RunID             string
	TargetFingerprint string
	AllowCrossRun     bool
	Now               time.Time
}

type KnowledgeEvent struct {
	Type string
	Data any
}

// Artifact and ArtifactInput are content-addressed evidence records.
type Artifact struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id"`
	AttemptID string    `json:"attempt_id"`
	SessionID string    `json:"session_id"`
	Name      string    `json:"name"`
	MediaType string    `json:"media_type"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	Redacted  bool      `json:"redacted,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type ArtifactInput struct {
	RunID     string
	AttemptID string
	SessionID string
	Name      string
	MediaType string
	Size      int64
	Content   io.Reader
}

// Verdict is the only successful run authority.
type Verdict struct {
	Success  bool            `json:"success"`
	Score    float64         `json:"score,omitempty"`
	Message  string          `json:"message,omitempty"`
	Evidence json.RawMessage `json:"evidence,omitempty"`
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

type SessionResult struct {
	ID       string `json:"id"`
	Status   Status `json:"status"`
	ExitCode int    `json:"exit_code,omitempty"`
	Usage    Usage  `json:"usage,omitempty"`
	Error    string `json:"error,omitempty"`
}

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

type VerdictRecord struct {
	RunID     string          `json:"run_id"`
	Success   bool            `json:"success"`
	Score     float64         `json:"score,omitempty"`
	Message   string          `json:"message,omitempty"`
	Evidence  json.RawMessage `json:"evidence,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type SweepRecord struct {
	ID         string          `json:"id"`
	Status     Status          `json:"status"`
	Spec       json.RawMessage `json:"spec,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
}

// AgentEvent is the normalized runtime event consumed by the turn protocol.
type AgentEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

type LogEvent struct {
	Stream string `json:"stream"`
	Text   string `json:"text"`
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
	Store         Store
}

type EvidenceArchiveRequest struct {
	RunID         string
	BranchID      string
	AttemptID     string
	SessionID     string
	WorkspaceDir  string
	Observations  []ObservationProposal
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
}

type KnowledgePromotionRequest struct {
	RunID        string
	AttemptID    string
	SessionID    string
	Task         Task
	Target       Target
	Branch       Branch
	Proposal     KnowledgeProposal
	Observations map[string]Observation
	Existing     []Knowledge
	Now          time.Time
}

type EvidenceArchiveRequestAlias = EvidenceArchiveRequest
