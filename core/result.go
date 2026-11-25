package core

import "time"

// RunResult is the immutable terminal snapshot the v5 kernel returns. It is
// derived from the persisted state and board, never from uncommitted work.
type RunResult struct {
	RunID        string          `json:"run_id"`
	Phase        Phase           `json:"phase"`
	Outcome      Outcome         `json:"outcome,omitempty"`
	Verdict      *Verdict        `json:"verdict,omitempty"`
	Claims       []Claim         `json:"claims,omitempty"`
	Intents      []Intent        `json:"intents,omitempty"`
	Attempts     []IntentAttempt `json:"attempts,omitempty"`
	Observations []Observation   `json:"observations,omitempty"`
	Artifacts    []Artifact      `json:"artifacts,omitempty"`
	Usage        Usage           `json:"usage,omitempty"`
	Error        string          `json:"error,omitempty"`
	StartedAt    *time.Time      `json:"started_at,omitempty"`
	FinishedAt   *time.Time      `json:"finished_at,omitempty"`
}

// ResultFrom projects a terminal state and board into a RunResult.
func ResultFrom(state State, board Board, verdict *Verdict) RunResult {
	return RunResult{
		RunID: state.RunID, Phase: state.Phase, Outcome: state.Outcome, Verdict: verdict,
		Claims: board.Claims, Intents: board.Intents, Attempts: board.Attempts,
		Observations: board.Observations, Artifacts: board.Artifacts,
		Usage: state.Usage, Error: state.Error,
		StartedAt: state.StartedAt, FinishedAt: state.FinishedAt,
	}
}
