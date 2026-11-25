package core

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Phase is the v5 Run lifecycle stage. Terminal is reachable only through a
// finalize transition; there is no separate "finalizing" phase.
type Phase string

const (
	PhaseQueued       Phase = "queued"
	PhaseProvisioning Phase = "provisioning"
	PhaseRunning      Phase = "running"
	PhaseVerifying    Phase = "verifying"
	PhaseTerminal     Phase = "terminal"
)

// Valid reports whether phase is a known v5 phase.
func (p Phase) Valid() bool {
	switch p {
	case PhaseQueued, PhaseProvisioning, PhaseRunning, PhaseVerifying, PhaseTerminal:
		return true
	default:
		return false
	}
}

// Terminal reports whether the phase is terminal.
func (p Phase) Terminal() bool { return p == PhaseTerminal }

// Outcome is the final disposition of a v5 Run. Only Succeeded is success.
type Outcome string

const (
	OutcomeSucceeded   Outcome = "succeeded"
	OutcomeFailed      Outcome = "failed"
	OutcomeCancelled   Outcome = "cancelled"
	OutcomeTimedOut    Outcome = "timed_out"
	OutcomeInterrupted Outcome = "interrupted"
	OutcomeStagnated   Outcome = "stagnated"
)

// Valid reports whether outcome is a known v5 outcome.
func (o Outcome) Valid() bool {
	switch o {
	case OutcomeSucceeded, OutcomeFailed, OutcomeCancelled, OutcomeTimedOut, OutcomeInterrupted, OutcomeStagnated:
		return true
	default:
		return false
	}
}

// Success reports whether the outcome is the only successful disposition.
func (o Outcome) Success() bool { return o == OutcomeSucceeded }

// State is the persisted v5 Run state. It is a projection of the journal; the
// journal is the source of truth.
type State struct {
	RunID           string     `json:"run_id"`
	Phase           Phase      `json:"phase"`
	Outcome         Outcome    `json:"outcome,omitempty"`
	HeadSeq         uint64     `json:"head_seq"`
	HeadHash        string     `json:"head_hash,omitempty"`
	BoardRevision   uint64     `json:"board_revision"`
	Usage           Usage      `json:"usage,omitempty"`
	GoalCandidate   bool       `json:"goal_candidate,omitempty"`
	NonGrowthRounds int        `json:"non_growth_rounds,omitempty"`
	ReasonFailures  int        `json:"reason_failures,omitempty"`
	Stagnated       bool       `json:"stagnated,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	Error           string     `json:"error,omitempty"`
}

// Validate rejects states that cannot exist in the v5 lifecycle.
func (s State) Validate() error {
	if strings.TrimSpace(s.RunID) == "" {
		return errors.New("state run_id is required")
	}
	if !s.Phase.Valid() {
		return fmt.Errorf("unknown phase %q", s.Phase)
	}
	if s.Phase == PhaseTerminal {
		if !s.Outcome.Valid() {
			return errors.New("terminal state requires a valid outcome")
		}
		if s.FinishedAt == nil || s.FinishedAt.IsZero() {
			return errors.New("terminal state requires finished_at")
		}
		if s.Outcome == OutcomeSucceeded && s.Stagnated {
			return errors.New("a successful state cannot be stagnated")
		}
	} else {
		if s.Outcome != "" {
			return fmt.Errorf("non-terminal state %q cannot have outcome %q", s.Phase, s.Outcome)
		}
		if s.FinishedAt != nil {
			return errors.New("non-terminal state cannot have finished_at")
		}
	}
	if s.HeadSeq == 0 {
		return errors.New("state head_seq must be at least 1")
	}
	if s.HeadHash != "" && len(s.HeadHash) != 64 {
		return errors.New("state head_hash must be 64 hex characters")
	}
	if s.NonGrowthRounds < 0 || s.ReasonFailures < 0 {
		return errors.New("state counters must not be negative")
	}
	if s.Stagnated && s.GoalCandidate {
		return errors.New("a stagnated state cannot also be a goal candidate")
	}
	if s.Usage.InputTokens < 0 || s.Usage.OutputTokens < 0 || s.Usage.TotalTokens < 0 ||
		s.Usage.ModelCalls < 0 || s.Usage.ToolCalls < 0 || s.Usage.CostUSD < 0 {
		return errors.New("usage values must not be negative")
	}
	return nil
}

// TransitionKind names the explicit lifecycle operations accepted by Advance.
type TransitionKind string

const (
	TransitionStart         TransitionKind = "start"
	TransitionProvisioned   TransitionKind = "provisioned"
	TransitionBoardRound    TransitionKind = "board_round"
	TransitionGoalCandidate TransitionKind = "goal_candidate"
	TransitionFinalize      TransitionKind = "finalize"
)

// Transition is one explicit state-machine operation.
type Transition struct {
	Kind TransitionKind

	At              time.Time
	HeadSeq         uint64
	BoardRevision   uint64
	Usage           Usage
	NonGrowthRounds int
	ReasonFailures  int
	Stagnated       bool

	Outcome Outcome
	Verdict *Verdict
	Error   string
}

// Advance applies one transition or rejects it. Terminal is immutable, and a
// successful terminal state requires a successful verifier verdict.
func Advance(state State, transition Transition) (State, error) {
	if err := state.Validate(); err != nil {
		return State{}, fmt.Errorf("invalid state: %w", err)
	}
	if state.Phase.Terminal() {
		return State{}, fmt.Errorf("run %s is terminal", state.RunID)
	}
	if transition.At.IsZero() {
		return State{}, errors.New("transition time is required")
	}
	if transition.Kind != TransitionFinalize && (transition.Outcome != "" || transition.Verdict != nil) {
		return State{}, errors.New("only finalize may carry an outcome or verdict")
	}
	next := state
	if transition.HeadSeq != 0 {
		if transition.HeadSeq <= state.HeadSeq {
			return State{}, fmt.Errorf("head_seq must advance: %d -> %d", state.HeadSeq, transition.HeadSeq)
		}
		next.HeadSeq = transition.HeadSeq
	}
	if transition.BoardRevision != 0 {
		if transition.BoardRevision < state.BoardRevision {
			return State{}, fmt.Errorf("board revision must not decrease: %d -> %d", state.BoardRevision, transition.BoardRevision)
		}
		next.BoardRevision = transition.BoardRevision
	}
	if transition.Usage != (Usage{}) {
		if !usageAtLeast(transition.Usage, state.Usage) {
			return State{}, errors.New("usage must not decrease")
		}
		next.Usage = transition.Usage
	}

	switch transition.Kind {
	case TransitionStart:
		if state.Phase != PhaseQueued {
			return State{}, fmt.Errorf("run cannot start from phase %q", state.Phase)
		}
		next.Phase = PhaseProvisioning
		if next.StartedAt == nil {
			started := transition.At.UTC()
			next.StartedAt = &started
		}
	case TransitionProvisioned:
		if state.Phase != PhaseProvisioning {
			return State{}, fmt.Errorf("run cannot become provisioned from phase %q", state.Phase)
		}
		next.Phase = PhaseRunning
	case TransitionBoardRound:
		if state.Phase != PhaseRunning {
			return State{}, fmt.Errorf("board round requires running phase, have %q", state.Phase)
		}
		next.NonGrowthRounds = transition.NonGrowthRounds
		next.ReasonFailures = transition.ReasonFailures
		next.Stagnated = state.Stagnated || transition.Stagnated
	case TransitionGoalCandidate:
		if state.Phase != PhaseRunning {
			return State{}, fmt.Errorf("goal candidate requires running phase, have %q", state.Phase)
		}
		if state.Stagnated {
			return State{}, errors.New("stagnated run cannot request verification")
		}
		next.GoalCandidate = true
		next.Phase = PhaseVerifying
	case TransitionFinalize:
		if !transition.Outcome.Valid() {
			return State{}, fmt.Errorf("finalize requires a valid outcome, have %q", transition.Outcome)
		}
		if transition.Outcome.Success() {
			if state.Phase != PhaseVerifying {
				return State{}, fmt.Errorf("success requires verifying phase, have %q", state.Phase)
			}
			if !state.GoalCandidate {
				return State{}, errors.New("success requires a goal candidate")
			}
			if transition.Verdict == nil || !transition.Verdict.Success {
				return State{}, errors.New("success requires a successful verifier verdict")
			}
			if transition.Verdict.Authority == VerdictAuthorityBackend {
				return State{}, errors.New("backend authority is not supported in v5")
			}
		}
		next.Phase = PhaseTerminal
		next.Outcome = transition.Outcome
		next.Error = transition.Error
		finished := transition.At.UTC()
		next.FinishedAt = &finished
	default:
		return State{}, fmt.Errorf("unknown transition kind %q", transition.Kind)
	}
	if err := next.Validate(); err != nil {
		return State{}, fmt.Errorf("transition produced invalid state: %w", err)
	}
	return next, nil
}

func usageAtLeast(next, previous Usage) bool {
	return next.InputTokens >= previous.InputTokens &&
		next.OutputTokens >= previous.OutputTokens &&
		next.TotalTokens >= previous.TotalTokens &&
		next.ModelCalls >= previous.ModelCalls &&
		next.ToolCalls >= previous.ToolCalls &&
		next.CostUSD >= previous.CostUSD
}
