package engine

import (
	"context"

	"github.com/red-copilot/harness/core"
)

// Run is a stable handle for one controller-owned Run. It exposes only
// cancellation, bounded hints and read-only projections; it owns no control state.
type Run struct {
	ID         string
	controller *runController
}

func newRun(controller *runController) *Run {
	return &Run{ID: controller.id, controller: controller}
}

func (r *Run) Events() <-chan Event  { return r.controller.events }
func (r *Run) Done() <-chan struct{} { return r.controller.done }

func (r *Run) Wait(ctx context.Context) (Result, error) {
	select {
	case <-r.controller.done:
		return r.controller.waitResult()
	case <-ctx.Done():
		return Result{RunID: r.ID, Phase: RunPhaseRunning}, ctx.Err()
	}
}

// Cancel only signals run-scoped cancellation. The controller observes it and
// remains the sole writer of the resulting terminal transition.
func (r *Run) Cancel(context.Context) error {
	r.controller.cancel()
	return nil
}

func (r *Run) SubmitHint(ctx context.Context, content, creator string) error {
	if r.controller.hints == nil {
		return ErrRunClosed
	}
	hint := core.Hint{Content: content, Creator: creator}
	select {
	case r.controller.hints <- hint:
		return nil
	case <-r.controller.ctx.Done():
		return r.controller.ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}
