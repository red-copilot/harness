package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// BranchStatus is the lifecycle state of one investigation branch.
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

// AttemptStatus describes how one bounded branch attempt ended.
type AttemptStatus string

const (
	AttemptRunning   AttemptStatus = "running"
	AttemptCompleted AttemptStatus = "completed"
	AttemptTruncated AttemptStatus = "truncated"
	AttemptFailed    AttemptStatus = "failed"
	AttemptCancelled AttemptStatus = "cancelled"
)

// AttemptOutcome is the local, non-authoritative result of one attempt.
type AttemptOutcome string

const (
	AttemptLocalSuccess       AttemptOutcome = "local_success"
	AttemptRefuted            AttemptOutcome = "refuted"
	AttemptBlocked            AttemptOutcome = "blocked"
	AttemptInconclusive       AttemptOutcome = "inconclusive"
	AttemptOperationalFailure AttemptOutcome = "operational_failure"
)

// KnowledgeKind classifies a durable conclusion.
type KnowledgeKind string

const (
	KnowledgeCapability KnowledgeKind = "capability"
	KnowledgeConstraint KnowledgeKind = "constraint"
	KnowledgeAnchor     KnowledgeKind = "anchor"
	KnowledgeConflict   KnowledgeKind = "conflict"
	KnowledgeProgress   KnowledgeKind = "progress"
)

// KnowledgeScope controls where a durable conclusion may be reused.
type KnowledgeScope string

const (
	KnowledgeScopeTask     KnowledgeScope = "task"
	KnowledgeScopeTarget   KnowledgeScope = "target"
	KnowledgeScopeInstance KnowledgeScope = "instance"
)

// KnowledgeTrust is the code-owned trust level of a durable conclusion.
type KnowledgeTrust string

const (
	KnowledgeObserved     KnowledgeTrust = "observed"
	KnowledgeCorroborated KnowledgeTrust = "corroborated"
	KnowledgeVerified     KnowledgeTrust = "verified"
	KnowledgeContested    KnowledgeTrust = "contested"
	KnowledgeRefuted      KnowledgeTrust = "refuted"
)

// ReusePolicy controls whether knowledge may cross a run boundary.
type ReusePolicy string

const (
	ReuseStable   ReusePolicy = "stable"
	ReuseInstance ReusePolicy = "instance"
	ReuseNever    ReusePolicy = "never"
)

// BranchBudget bounds one attempt of a branch.
type BranchBudget struct {
	MaxAttempts  int           `json:"max_attempts,omitempty" yaml:"max_attempts,omitempty"`
	MaxToolCalls int           `json:"max_tool_calls,omitempty" yaml:"max_tool_calls,omitempty"`
	MaxWallTime  time.Duration `json:"max_wall_time,omitempty" yaml:"max_wall_time,omitempty"`
}

// Branch is the long-lived investigation unit in the v2 exploration tree.
type Branch struct {
	ID               string       `json:"id"`
	ParentID         string       `json:"parent_id,omitempty"`
	TaskID           string       `json:"task_id"`
	RunID            string       `json:"run_id,omitempty"`
	Depth            int          `json:"depth"`
	Topic            string       `json:"topic"`
	Goal             string       `json:"goal"`
	Hypothesis       string       `json:"hypothesis"`
	SuccessCondition string       `json:"success_condition"`
	FailureCondition string       `json:"failure_condition"`
	Scope            string       `json:"scope"`
	Budget           BranchBudget `json:"budget,omitempty"`
	Status           BranchStatus `json:"status"`
	Priority         int          `json:"priority,omitempty"`
	KnowledgeRefs    []string     `json:"knowledge_refs,omitempty"`
	CreatedAt        time.Time    `json:"created_at,omitempty"`
	UpdatedAt        time.Time    `json:"updated_at,omitempty"`
}

// BranchProposal is a model-proposed branch. The engine validates it before
// adding it to the tree.
type BranchProposal struct {
	ParentID         string `json:"parent_id,omitempty"`
	Topic            string `json:"topic"`
	Goal             string `json:"goal"`
	Hypothesis       string `json:"hypothesis"`
	SuccessCondition string `json:"success_condition"`
	FailureCondition string `json:"failure_condition"`
	Target           string `json:"target"`
	Priority         int    `json:"priority,omitempty"`
}

// Observation is an immutable, bounded record of what one attempt observed.
type Observation struct {
	ID           string    `json:"id"`
	BranchID     string    `json:"branch_id"`
	AttemptID    string    `json:"attempt_id"`
	Kind         string    `json:"kind"`
	Action       string    `json:"action"`
	Target       string    `json:"target"`
	Summary      string    `json:"summary"`
	Data         []byte    `json:"data,omitempty"`
	EvidenceRefs []string  `json:"evidence_refs,omitempty"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
}

// Attempt is one bounded execution of a Branch.
type Attempt struct {
	ID             string         `json:"id"`
	BranchID       string         `json:"branch_id"`
	RunID          string         `json:"run_id,omitempty"`
	SessionID      string         `json:"session_id,omitempty"`
	Status         AttemptStatus  `json:"status"`
	Outcome        AttemptOutcome `json:"outcome,omitempty"`
	ObservationIDs []string       `json:"observation_ids,omitempty"`
	StartedAt      time.Time      `json:"started_at,omitempty"`
	FinishedAt     time.Time      `json:"finished_at,omitempty"`
	Error          string         `json:"error,omitempty"`
}

// KnowledgeProposal is a model-proposed durable conclusion.
type KnowledgeProposal struct {
	Topic        string         `json:"topic"`
	Kind         KnowledgeKind  `json:"kind"`
	Statement    string         `json:"statement"`
	Scope        KnowledgeScope `json:"scope"`
	Target       string         `json:"target,omitempty"`
	EvidenceRefs []string       `json:"evidence_refs"`
	Supports     []string       `json:"supports,omitempty"`
	Refutes      []string       `json:"refutes,omitempty"`
	Reuse        ReusePolicy    `json:"reuse"`
}

// Knowledge is a code-promoted, deduplicated durable conclusion.
type Knowledge struct {
	ID            string         `json:"id"`
	TaskID        string         `json:"task_id"`
	RunID         string         `json:"run_id,omitempty"`
	Topic         string         `json:"topic"`
	Kind          KnowledgeKind  `json:"kind"`
	Statement     string         `json:"statement"`
	Scope         KnowledgeScope `json:"scope"`
	Target        string         `json:"target,omitempty"`
	Trust         KnowledgeTrust `json:"trust"`
	Reuse         ReusePolicy    `json:"reuse"`
	EvidenceRefs  []string       `json:"evidence_refs,omitempty"`
	Sources       []string       `json:"sources,omitempty"`
	Contradicts   []string       `json:"contradicts,omitempty"`
	ValidFrom     time.Time      `json:"valid_from,omitempty"`
	LastValidated time.Time      `json:"last_validated_at,omitempty"`
	ExpiresAt     time.Time      `json:"expires_at,omitempty"`
	Version       int64          `json:"version,omitempty"`
}

// ExplorationPolicy bounds the binary hypothesis tree.
type ExplorationPolicy struct {
	// Legacy fields are retained only until the v2 runner migration is
	// complete. They are not used by the binary-tree engine.
	Rounds           int      `json:"rounds,omitempty" yaml:"rounds,omitempty"`
	MaxWorkers       int      `json:"max_workers,omitempty" yaml:"max_workers,omitempty"`
	PlannerAgentID   string   `json:"planner_agent_id,omitempty" yaml:"planner_agent_id,omitempty"`
	ExecutorAgentIDs []string `json:"executor_agent_ids,omitempty" yaml:"executor_agent_ids,omitempty"`

	MaxDepth               int           `json:"max_depth,omitempty" yaml:"max_depth,omitempty"`
	MaxNodes               int           `json:"max_nodes,omitempty" yaml:"max_nodes,omitempty"`
	MaxChildrenPerBranch   int           `json:"max_children_per_branch,omitempty" yaml:"max_children_per_branch,omitempty"`
	MaxActiveBranches      int           `json:"max_active_branches,omitempty" yaml:"max_active_branches,omitempty"`
	MaxAttemptsPerBranch   int           `json:"max_attempts_per_branch,omitempty" yaml:"max_attempts_per_branch,omitempty"`
	MaxToolCallsPerAttempt int           `json:"max_tool_calls_per_attempt,omitempty" yaml:"max_tool_calls_per_attempt,omitempty"`
	MaxWallTimePerAttempt  time.Duration `json:"max_wall_time_per_attempt,omitempty" yaml:"max_wall_time_per_attempt,omitempty"`
	StagnationRounds       int           `json:"stagnation_rounds,omitempty" yaml:"stagnation_rounds,omitempty"`
}

// DefaultExplorationPolicy returns the v2 binary-tree defaults.
func DefaultExplorationPolicy() ExplorationPolicy {
	return ExplorationPolicy{
		MaxDepth:               4,
		MaxNodes:               15,
		MaxChildrenPerBranch:   2,
		MaxActiveBranches:      2,
		MaxAttemptsPerBranch:   3,
		MaxToolCallsPerAttempt: 20,
		MaxWallTimePerAttempt:  10 * time.Minute,
		StagnationRounds:       3,
	}
}

// NormalizeExplorationPolicy fills zero fields with v2 defaults.
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
	return policy
}

// ValidateBranchProposal validates a branch before the engine adds it.
func ValidateBranchProposal(
	proposal BranchProposal,
	parent *Branch,
	targets []Target,
	existing []Branch,
	policy ExplorationPolicy,
) (Branch, error) {
	topic := normalizeTopic(proposal.Topic)
	if topic == "" {
		return Branch{}, fmt.Errorf("branch topic is required")
	}
	target := strings.TrimSpace(proposal.Target)
	if target == "" || !authorizedTargetV2(target, targets) {
		return Branch{}, fmt.Errorf("branch target %q is not authorized", target)
	}
	if parent == nil {
		return Branch{}, fmt.Errorf("branch parent is required")
	}
	if parent.Depth+1 > policy.MaxDepth {
		return Branch{}, fmt.Errorf("branch depth %d exceeds max depth %d", parent.Depth+1, policy.MaxDepth)
	}
	if len(existing)+1 > policy.MaxNodes {
		return Branch{}, fmt.Errorf("branch count %d exceeds max nodes %d", len(existing)+1, policy.MaxNodes)
	}
	for _, branch := range existing {
		if normalizeTopic(branch.Topic) == topic && strings.TrimSpace(branch.Scope) == target {
			return Branch{}, fmt.Errorf("duplicate branch topic+scope %q/%q", topic, target)
		}
	}
	now := time.Now().UTC()
	branch := Branch{
		ID:               "branch-" + shortHash(topic+"\x00"+target),
		ParentID:         parent.ID,
		TaskID:           parent.TaskID,
		RunID:            parent.RunID,
		Depth:            parent.Depth + 1,
		Topic:            topic,
		Goal:             strings.TrimSpace(proposal.Goal),
		Hypothesis:       strings.TrimSpace(proposal.Hypothesis),
		SuccessCondition: strings.TrimSpace(proposal.SuccessCondition),
		FailureCondition: strings.TrimSpace(proposal.FailureCondition),
		Scope:            target,
		Status:           BranchOpen,
		Priority:         proposal.Priority,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	return branch, nil
}

// ValidateKnowledgeProposal validates and canonicalizes a knowledge proposal.
func ValidateKnowledgeProposal(
	proposal KnowledgeProposal,
	branch Branch,
	knownEvidence map[string]struct{},
	existing []Knowledge,
) (Knowledge, error) {
	topic := normalizeTopic(proposal.Topic)
	if topic == "" {
		return Knowledge{}, fmt.Errorf("knowledge topic is required")
	}
	if strings.TrimSpace(proposal.Statement) == "" {
		return Knowledge{}, fmt.Errorf("knowledge statement is required")
	}
	if len(proposal.EvidenceRefs) == 0 {
		return Knowledge{}, fmt.Errorf("knowledge evidence is required")
	}
	for _, ref := range proposal.EvidenceRefs {
		if _, ok := knownEvidence[ref]; !ok {
			return Knowledge{}, fmt.Errorf("knowledge evidence %q is unknown", ref)
		}
	}
	if containsSecretLikeValue(proposal.Statement) {
		return Knowledge{}, fmt.Errorf("knowledge statement contains a secret value")
	}
	candidate := Knowledge{
		TaskID:       branch.TaskID,
		RunID:        branch.RunID,
		Topic:        topic,
		Kind:         proposal.Kind,
		Statement:    strings.TrimSpace(proposal.Statement),
		Scope:        proposal.Scope,
		Target:       strings.TrimSpace(proposal.Target),
		Trust:        KnowledgeObserved,
		Reuse:        proposal.Reuse,
		EvidenceRefs: append([]string(nil), proposal.EvidenceRefs...),
		Contradicts:  append([]string(nil), proposal.Refutes...),
		ValidFrom:    time.Now().UTC(),
	}
	fingerprint := KnowledgeFingerprint(candidate)
	candidate.ID = "knowledge-" + fingerprint[:16]
	for _, item := range existing {
		if KnowledgeFingerprint(item) == fingerprint {
			return Knowledge{}, fmt.Errorf("duplicate knowledge topic+scope+kind %q", fingerprint)
		}
	}
	return candidate, nil
}

// KnowledgeFingerprint is the coarse deduplication identity for knowledge.
func KnowledgeFingerprint(knowledge Knowledge) string {
	payload := strings.Join([]string{
		normalizeTopic(knowledge.Topic),
		string(knowledge.Scope),
		string(knowledge.Kind),
	}, "\x00")
	return shortHash(payload)
}

// CanExpandBranch reports whether a branch may create two child branches.
func CanExpandBranch(branch Branch, attempt Attempt) bool {
	if branch.Status == BranchRefuted || branch.Status == BranchBlocked || branch.Status == BranchAbandoned {
		return false
	}
	return attempt.Status == AttemptCompleted &&
		attempt.Outcome == AttemptLocalSuccess &&
		len(attempt.ObservationIDs) > 0
}

func normalizeTopic(topic string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(topic)), "."))
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func authorizedTargetV2(target string, targets []Target) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	targetHost := targetHostV2(target)
	for _, authorized := range targets {
		if authorized.ID != "" && strings.EqualFold(target, authorized.ID) {
			return true
		}
		for _, address := range authorized.Addresses {
			if strings.EqualFold(target, address) {
				return true
			}
			if targetHost != "" && strings.EqualFold(targetHost, targetHostV2(address)) {
				return true
			}
		}
	}
	return false
}

func targetHostV2(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		if parsed, err := url.Parse(value); err == nil {
			return strings.ToLower(parsed.Hostname())
		}
	}
	if strings.Contains(value, ":") {
		if parsed, err := url.Parse("//" + value); err == nil {
			return strings.ToLower(parsed.Hostname())
		}
	}
	return strings.ToLower(value)
}

func containsSecretLikeValue(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{"password=", "token=", "flag{", "ctf{", "tsec{", "benchmark_token", "authorization: bearer"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
