package kernel

import (
	"context"
	"sync"

	"github.com/red-copilot/harness/core"
)

// Run is a stable handle for one controller-owned v5 Run.
type Run struct {
	ID     string
	active *activeRun
}

// Events streams persisted journal records. It never blocks; slow consumers
// drop live records that remain available from the store.
func (r *Run) Events() <-chan core.Record { return r.active.events }

// Done is closed after the terminal state has been persisted.
func (r *Run) Done() <-chan struct{} { return r.active.done }

// Wait returns the immutable terminal snapshot.
func (r *Run) Wait(ctx context.Context) (core.RunResult, error) {
	select {
	case <-r.active.done:
		return r.active.resultSnapshot()
	case <-ctx.Done():
		state := r.active.writer.stateSnapshot()
		return core.RunResult{RunID: r.ID, Phase: state.Phase, Outcome: state.Outcome}, ctx.Err()
	}
}

// Cancel signals run-scoped cancellation; the controller remains the only
// writer of the terminal transition.
func (r *Run) Cancel(context.Context) error {
	r.active.cancel()
	return nil
}

type activeSlot struct {
	cancel  context.CancelFunc
	intent  core.Intent
	attempt core.IntentAttempt
	mu      sync.Mutex
	done    bool
}

type attemptResult struct {
	intent     core.Intent
	attempt    core.IntentAttempt
	report     core.AttemptReport
	conclusion string
	usage      core.Usage
	err        error
}

type reasonResult struct {
	report        core.ReasonReport
	boardRevision uint64
	usage         core.Usage
	err           error
}
