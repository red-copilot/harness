package engine

import (
	"context"
)

// Run is a stable handle for one controller-owned Run. It exposes only
// cancellation, steering and read-only projections; it owns no control state.
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
		return Result{RunID: r.ID, Phase: RunPhaseRunning, Status: StatusRunning}, ctx.Err()
	}
}

// Cancel only signals run-scoped cancellation. The controller observes it and
// remains the sole writer of the resulting terminal transition.
func (r *Run) Cancel(context.Context) error {
	r.controller.cancel()
	return nil
}

func (r *Run) Steer(ctx context.Context, sessionID, message string) error {
	return r.controller.steer(ctx, sessionID, message)
}
