package harness

import (
	"time"
)

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
	// MaxItems is the mechanically enforced knowledge cap. Zero falls back to
	// DefaultExplorationPolicy().MaxKnowledgeItems.
	MaxItems int
	Now      time.Time
}
