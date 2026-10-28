package core

import (
	"fmt"
	"strings"
	"time"
)

// FactKind describes why a Fact exists. Facts are immutable board records.
type FactKind string

const (
	FactOrigin   FactKind = "origin"
	FactGoal     FactKind = "goal"
	FactFinding  FactKind = "finding"
	FactNegative FactKind = "negative"
)

// Fact is a confirmed state-space fact. Finding facts require evidence.
type Fact struct {
	ID             string    `json:"id"`
	RunID          string    `json:"run_id"`
	TaskID         string    `json:"task_id"`
	Kind           FactKind  `json:"kind"`
	Description    string    `json:"description"`
	SourceIntentID string    `json:"source_intent_id,omitempty"`
	ObservationIDs []string  `json:"observation_ids,omitempty"`
	ArtifactIDs    []string  `json:"artifact_ids,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// IntentKind distinguishes the initial direct solve from graph exploration.
type IntentKind string

const (
	IntentBootstrap IntentKind = "bootstrap"
	IntentExplore   IntentKind = "explore"
)

// IntentState is the lifecycle of one declared exploration direction.
type IntentState string

const (
	IntentOpen      IntentState = "open"
	IntentClaimed   IntentState = "claimed"
	IntentConcluded IntentState = "concluded"
	IntentAbandoned IntentState = "abandoned"
)

// Intent is a declared direction backed by one or more existing Facts. It is
// not a tree node and does not produce child Intents directly.
type Intent struct {
	ID           string      `json:"id"`
	RunID        string      `json:"run_id"`
	Kind         IntentKind  `json:"kind"`
	FromFactIDs  []string    `json:"from_fact_ids"`
	Description  string      `json:"description"`
	State        IntentState `json:"state"`
	Slot         int         `json:"slot,omitempty"`
	ExecutorID   string      `json:"executor_id,omitempty"`
	AttemptIDs   []string    `json:"attempt_ids,omitempty"`
	ResultFactID string      `json:"result_fact_id,omitempty"`
	CreatedAt    time.Time   `json:"created_at"`
	ClaimedAt    *time.Time  `json:"claimed_at,omitempty"`
	ConcludedAt  *time.Time  `json:"concluded_at,omitempty"`
	Error        string      `json:"error,omitempty"`
}

// Hint is read-only human or platform guidance.
type Hint struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id"`
	Content   string    `json:"content"`
	Creator   string    `json:"creator"`
	CreatedAt time.Time `json:"created_at"`
}

// IntentAttemptPhase describes execution of one Intent attempt.
type IntentAttemptPhase string

const (
	IntentAttemptRunning   IntentAttemptPhase = "running"
	IntentAttemptConcluded IntentAttemptPhase = "concluded"
	IntentAttemptFailed    IntentAttemptPhase = "failed"
	IntentAttemptCancelled IntentAttemptPhase = "cancelled"
	IntentAttemptTimedOut  IntentAttemptPhase = "timed_out"
)

// IntentAttempt is one execution of an Intent. Execute and conclude are phases
// of the same attempt; they are not separate frontier entries.
type IntentAttempt struct {
	ID         string             `json:"id"`
	RunID      string             `json:"run_id"`
	IntentID   string             `json:"intent_id"`
	Slot       int                `json:"slot"`
	SessionID  string             `json:"session_id"`
	Phase      IntentAttemptPhase `json:"phase"`
	Conclusion string             `json:"conclusion,omitempty"`
	StartedAt  time.Time          `json:"started_at"`
	FinishedAt time.Time          `json:"finished_at"`
	Error      string             `json:"error,omitempty"`
}

// FactProposal is model-proposed semantic content. The controller validates it
// and generates the real Fact identity.
type FactProposal struct {
	Kind            FactKind `json:"kind"`
	Description     string   `json:"description"`
	ObservationRefs []string `json:"observation_refs,omitempty"`
}

// ExploreReport is the only structured output of an Explore attempt.
type ExploreReport struct {
	IntentID     string                `json:"intent_id"`
	Phase        IntentAttemptPhase    `json:"phase"`
	Fact         *FactProposal         `json:"fact,omitempty"`
	Observations []ObservationProposal `json:"observations,omitempty"`
	Error        string                `json:"error,omitempty"`
}

// IntentProposal is one Reason-proposed direction.
type IntentProposal struct {
	From        []string `json:"from"`
	Description string   `json:"description"`
}

// GoalCandidate proposes a verifier invocation. It is not a success verdict.
type GoalCandidate struct {
	From        []string `json:"from"`
	Description string   `json:"description"`
}

// ReasonReport is the strict Reason task output.
type ReasonReport struct {
	FrontierVersion uint64           `json:"frontier_version"`
	Intents         []IntentProposal `json:"intents,omitempty"`
	GoalCandidate   *GoalCandidate   `json:"goal_candidate,omitempty"`
}

// ReasonRequest is the bounded Reason context.
type ReasonRequest struct {
	Task            Task          `json:"task"`
	Target          TargetBinding `json:"target"`
	Facts           []Fact        `json:"facts"`
	Intents         []Intent      `json:"intents"`
	Hints           []Hint        `json:"hints"`
	FreeSlots       []int         `json:"free_slots"`
	FrontierVersion uint64        `json:"frontier_version"`
	RecentFacts     []Fact        `json:"recent_facts,omitempty"`
	Progress        Completion    `json:"progress"`
	Policy          RunPolicy     `json:"-"`
}

// ExploreRequest is the bounded Explore context.
type ExploreRequest struct {
	Task            Task          `json:"task"`
	Target          TargetBinding `json:"target"`
	Intent          Intent        `json:"intent"`
	Facts           []Fact        `json:"facts"`
	Hints           []Hint        `json:"hints"`
	Budget          AttemptBudget `json:"budget"`
	WorkspaceDir    string        `json:"workspace_dir"`
	MaxReportBytes  int64         `json:"max_report_bytes"`
	FrontierVersion uint64        `json:"frontier_version"`
}

// ReasonStrategy produces and parses one Reason turn.
type ReasonStrategy interface {
	Prompt(ReasonRequest) (string, error)
	Parse(ReasonRequest, TurnResult) (ReasonReport, error)
}

// ExploreStrategy produces execute/conclude prompts and parses Explore output.
type ExploreStrategy interface {
	ExecutePrompt(ExploreRequest) (string, error)
	ConcludePrompt(ExploreRequest) (string, error)
	Parse(ExploreRequest, TurnResult) (ExploreReport, error)
}

// ValidateFact enforces immutable Fact shape and evidence requirements.
func ValidateFact(fact Fact) error {
	if strings.TrimSpace(fact.ID) == "" {
		return fmt.Errorf("fact id is required")
	}
	if strings.TrimSpace(fact.RunID) == "" {
		return fmt.Errorf("fact run_id is required")
	}
	if strings.TrimSpace(fact.TaskID) == "" {
		return fmt.Errorf("fact task_id is required")
	}
	if strings.TrimSpace(fact.Description) == "" {
		return fmt.Errorf("fact description is required")
	}
	switch fact.Kind {
	case FactOrigin, FactGoal:
		return nil
	case FactFinding:
		if len(fact.ObservationIDs) == 0 {
			return fmt.Errorf("finding fact requires observations")
		}
	case FactNegative:
		return nil
	default:
		return fmt.Errorf("unknown fact kind %q", fact.Kind)
	}
	return nil
}

// ValidateIntentTransition enforces the Intent lifecycle.
func ValidateIntentTransition(from, to IntentState) error {
	switch from {
	case "":
		if to == IntentOpen {
			return nil
		}
	case IntentOpen:
		if to == IntentClaimed || to == IntentAbandoned {
			return nil
		}
	case IntentClaimed:
		if to == IntentOpen || to == IntentConcluded || to == IntentAbandoned {
			return nil
		}
	case IntentConcluded, IntentAbandoned:
		return fmt.Errorf("intent state %q is terminal", from)
	default:
		return fmt.Errorf("unknown intent state %q", from)
	}
	return fmt.Errorf("invalid intent transition %q -> %q", from, to)
}

// ValidateIntent checks a complete Intent value.
func ValidateIntent(intent Intent) error {
	if strings.TrimSpace(intent.ID) == "" || strings.TrimSpace(intent.RunID) == "" {
		return fmt.Errorf("intent id and run_id are required")
	}
	if intent.Kind != IntentBootstrap && intent.Kind != IntentExplore {
		return fmt.Errorf("unknown intent kind %q", intent.Kind)
	}
	if len(intent.FromFactIDs) == 0 || strings.TrimSpace(intent.Description) == "" {
		return fmt.Errorf("intent requires source facts and description")
	}
	if intent.State != IntentOpen && intent.State != IntentClaimed && intent.State != IntentConcluded && intent.State != IntentAbandoned {
		return fmt.Errorf("unknown intent state %q", intent.State)
	}
	if intent.State == IntentClaimed && (intent.Slot != 1 && intent.Slot != 2) {
		return fmt.Errorf("claimed intent requires slot 1 or 2")
	}
	if intent.State == IntentConcluded && strings.TrimSpace(intent.ResultFactID) == "" {
		return fmt.Errorf("concluded intent requires result fact")
	}
	return nil
}

// ValidateReasonReport rejects reports that could create more work than the
// controller can admit.
func ValidateReasonReport(report ReasonReport, freeSlots int) error {
	if freeSlots < 0 || freeSlots > 2 {
		return fmt.Errorf("free slots must be between 0 and 2")
	}
	if len(report.Intents) > freeSlots {
		return fmt.Errorf("reason proposed %d intents for %d free slots", len(report.Intents), freeSlots)
	}
	if len(report.Intents) > 0 && report.GoalCandidate != nil {
		return fmt.Errorf("reason cannot propose intents and a goal candidate together")
	}
	for i, intent := range report.Intents {
		if len(intent.From) == 0 || strings.TrimSpace(intent.Description) == "" {
			return fmt.Errorf("intent %d requires from facts and description", i)
		}
	}
	if report.GoalCandidate != nil && (len(report.GoalCandidate.From) == 0 || strings.TrimSpace(report.GoalCandidate.Description) == "") {
		return fmt.Errorf("goal candidate requires from facts and description")
	}
	return nil
}

// ValidateExploreReport rejects model-created origins/goals and unevidenced
// positive findings.
func ValidateExploreReport(report ExploreReport) error {
	if strings.TrimSpace(report.IntentID) == "" {
		return fmt.Errorf("explore report intent_id is required")
	}
	if report.Phase != IntentAttemptConcluded && report.Phase != IntentAttemptFailed {
		return fmt.Errorf("explore report phase %q is invalid", report.Phase)
	}
	if report.Fact == nil {
		if report.Phase == IntentAttemptFailed || strings.TrimSpace(report.Error) != "" {
			return nil
		}
		return fmt.Errorf("concluded explore report requires a fact")
	}
	switch report.Fact.Kind {
	case FactFinding:
		if len(report.Fact.ObservationRefs) == 0 {
			return fmt.Errorf("finding fact requires observation refs")
		}
	case FactNegative:
	default:
		return fmt.Errorf("explore fact kind %q is not allowed", report.Fact.Kind)
	}
	if strings.TrimSpace(report.Fact.Description) == "" {
		return fmt.Errorf("explore fact description is required")
	}
	return nil
}
