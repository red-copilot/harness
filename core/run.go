package core

import (
	"encoding/json"
	"fmt"
	"time"
)

// RunPhase is the non-terminal lifecycle stage.
type RunPhase string

const (
	RunPhaseQueued       RunPhase = "queued"
	RunPhaseProvisioning RunPhase = "provisioning"
	RunPhaseRunning      RunPhase = "running"
	RunPhaseFinalizing   RunPhase = "finalizing"
	RunPhaseVerifying    RunPhase = "verifying"
	RunPhaseTerminal     RunPhase = "terminal"
)

// RunOutcome is the final disposition of a Run.
type RunOutcome string

const (
	RunOutcomeSucceeded   RunOutcome = "succeeded"
	RunOutcomeFailed      RunOutcome = "failed"
	RunOutcomeCancelled   RunOutcome = "cancelled"
	RunOutcomeTimedOut    RunOutcome = "timed_out"
	RunOutcomeInterrupted RunOutcome = "interrupted"
	RunOutcomeStagnated   RunOutcome = "stagnated"
)

// ValidateRunState enforces the phase/outcome invariant.
func ValidateRunState(phase RunPhase, outcome RunOutcome) error {
	if phase == RunPhaseTerminal {
		switch outcome {
		case RunOutcomeSucceeded, RunOutcomeFailed, RunOutcomeCancelled, RunOutcomeTimedOut, RunOutcomeInterrupted, RunOutcomeStagnated:
			return nil
		default:
			return fmt.Errorf("terminal run requires a valid outcome")
		}
	}
	switch phase {
	case RunPhaseQueued, RunPhaseProvisioning, RunPhaseRunning, RunPhaseFinalizing, RunPhaseVerifying:
		if outcome != "" {
			return fmt.Errorf("non-terminal run %q cannot have outcome %q", phase, outcome)
		}
		return nil
	default:
		return fmt.Errorf("unknown run phase %q", phase)
	}
}

// ValidateRunTransition rejects phase rewind and undefined phase jumps.
func ValidateRunTransition(from, to RunPhase) error {
	if from == to && from != RunPhaseTerminal {
		return nil
	}
	allowed := map[RunPhase]map[RunPhase]bool{
		RunPhaseQueued:       {RunPhaseProvisioning: true, RunPhaseFinalizing: true, RunPhaseTerminal: true},
		RunPhaseProvisioning: {RunPhaseRunning: true, RunPhaseFinalizing: true, RunPhaseTerminal: true},
		RunPhaseRunning:      {RunPhaseFinalizing: true, RunPhaseVerifying: true, RunPhaseTerminal: true},
		RunPhaseFinalizing:   {RunPhaseVerifying: true, RunPhaseTerminal: true},
		RunPhaseVerifying:    {RunPhaseFinalizing: true, RunPhaseTerminal: true},
		RunPhaseTerminal:     {},
	}
	if allowed[from][to] {
		return nil
	}
	return fmt.Errorf("invalid run phase transition %q -> %q", from, to)
}

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

// Result is the immutable terminal snapshot returned after a Run terminates.
type Result struct {
	RunID          string          `json:"run_id"`
	Phase          RunPhase        `json:"phase"`
	Outcome        RunOutcome      `json:"outcome,omitempty"`
	Verdict        *Verdict        `json:"verdict,omitempty"`
	Claims         []Claim         `json:"claims,omitempty"`
	Intents        []Intent        `json:"intents,omitempty"`
	IntentAttempts []IntentAttempt `json:"intent_attempts,omitempty"`
	Observations   []Observation   `json:"observations,omitempty"`
	Artifacts      []Artifact      `json:"artifacts,omitempty"`
	Sessions       []SessionResult `json:"sessions,omitempty"`
	Usage          Usage           `json:"usage,omitempty"`
	Error          string          `json:"error,omitempty"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	FinishedAt     *time.Time      `json:"finished_at,omitempty"`
}

// CurrentSchemaVersion is the only descriptor schema this build accepts.
const CurrentSchemaVersion = 4

// ValidateDescriptor rejects descriptors from unknown schema generations.
func ValidateDescriptor(descriptor RunDescriptor) error {
	if descriptor.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("unsupported descriptor schema_version %d (want %d)", descriptor.SchemaVersion, CurrentSchemaVersion)
	}
	if descriptor.Task.ID == "" || descriptor.Task.Objective == "" {
		return fmt.Errorf("descriptor task id and objective are required")
	}
	if descriptor.Target.ID == "" {
		return fmt.Errorf("descriptor target id is required")
	}
	if descriptor.Reasoner.ID == "" {
		return fmt.Errorf("descriptor reasoner id is required")
	}
	if descriptor.Executor.ID == "" {
		return fmt.Errorf("descriptor executor id is required")
	}
	return ValidateRunPolicy(NormalizeRunPolicy(descriptor.Policy))
}

// RunDescriptor is the serializable portion of a Run.
type RunDescriptor struct {
	SchemaVersion int             `json:"schema_version" yaml:"schema_version"`
	Task          Task            `json:"task" yaml:"task"`
	Target        TargetSpec      `json:"target" yaml:"target"`
	Reasoner      AgentDescriptor `json:"reasoner" yaml:"reasoner"`
	Executor      AgentDescriptor `json:"executor" yaml:"executor"`
	Policy        RunPolicy       `json:"policy" yaml:"policy"`
}

// Event and LogRecord are canonical append-only audit records.
type Event struct {
	Seq           int64           `json:"seq"`
	SchemaVersion int             `json:"schema_version"`
	RunID         string          `json:"run_id"`
	SessionID     string          `json:"session_id,omitempty"`
	Source        string          `json:"source"`
	Type          EventType       `json:"type"`
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
	Phase      RunPhase        `json:"phase"`
	Outcome    RunOutcome      `json:"outcome,omitempty"`
	Spec       json.RawMessage `json:"spec,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	StartedAt  *time.Time      `json:"started_at,omitempty"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
}

type VerdictRecord struct {
	RunID     string           `json:"run_id"`
	Authority VerdictAuthority `json:"authority"`
	Success   bool             `json:"success"`
	Score     float64          `json:"score,omitempty"`
	Message   string           `json:"message,omitempty"`
	Evidence  json.RawMessage  `json:"evidence,omitempty"`
	CreatedAt time.Time        `json:"created_at"`
}

type LogEvent struct {
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

type ProvisionRequest struct {
	RunID    string
	Task     Task
	Target   TargetSpec
	Metadata map[string]string
}

type VerifyRequest struct {
	RunID       string
	Task        Task
	Target      TargetBinding
	EvidenceDir string
}
