package harness

import (
	"time"
)

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

// AttemptBudget bounds one attempt of a branch.
type AttemptBudget struct {
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
	// MaxReportBytes is the policy-enforced executor report cap.
	MaxReportBytes int64 `json:"max_report_bytes"`
}
