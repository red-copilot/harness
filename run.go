package harness

import (
	"context"
	"errors"
	"sync"
)

var ErrRunNotFound = errors.New("run not found")

// Run is a live handle for one asynchronous harness run.
type Run struct {
	ID string

	runner *Runner
	ctx    context.Context
	cancel context.CancelFunc

	events chan Event
	done   chan struct{}

	mu        sync.RWMutex
	sessions  map[string]AgentSession
	result    Result
	err       error
	budgetErr string
}

func newRun(runner *Runner, id string, ctx context.Context, cancel context.CancelFunc) *Run {
	return &Run{
		ID:       id,
		runner:   runner,
		ctx:      ctx,
		cancel:   cancel,
		events:   make(chan Event, 256),
		done:     make(chan struct{}),
		sessions: map[string]AgentSession{},
	}
}

// Events returns the live canonical event stream. Slow consumers may miss live
// notifications; complete history remains in the Store and can be replayed.
func (r *Run) Events() <-chan Event { return r.events }

// Wait waits for the run to finish or for ctx to be canceled.
func (r *Run) Wait(ctx context.Context) (Result, error) {
	select {
	case <-r.done:
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.result, r.err
	case <-ctx.Done():
		return Result{RunID: r.ID, Status: StatusRunning}, ctx.Err()
	}
}

// Cancel requests cancellation of the run and all sessions.
func (r *Run) Cancel(context.Context) error {
	r.cancel()
	return nil
}

// Steer sends an operator message to one live agent session.
func (r *Run) Steer(ctx context.Context, sessionID, message string) error {
	r.mu.RLock()
	session, ok := r.sessions[sessionID]
	r.mu.RUnlock()
	if !ok {
		return ErrRunNotFound
	}
	return session.Steer(ctx, message)
}

func (r *Run) addSession(id string, session AgentSession) {
	r.mu.Lock()
	r.sessions[id] = session
	r.mu.Unlock()
}

func (r *Run) snapshotSessions() map[string]AgentSession {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]AgentSession, len(r.sessions))
	for id, session := range r.sessions {
		out[id] = session
	}
	return out
}

func (r *Run) setBudgetExceeded(reason string) {
	r.mu.Lock()
	if r.budgetErr == "" {
		r.budgetErr = reason
	}
	r.mu.Unlock()
}

func (r *Run) budgetExceeded() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.budgetErr
}

func (r *Run) finish(result Result, err error) {
	r.mu.Lock()
	r.result = result
	r.err = err
	close(r.done)
	close(r.events)
	r.mu.Unlock()
}
