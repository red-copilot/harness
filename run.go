package harness

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrRunNotFound = errors.New("run not found")
	ErrRunClosed   = errors.New("run event stream closed")
)

// Run is a live handle for one Run.
type Run struct {
	ID string

	runner *Runner
	ctx    context.Context
	cancel context.CancelFunc
	events chan Event
	done   chan struct{}

	root      string
	taskDir   string
	skillsDir string
	startedAt *time.Time
	redaction *Redactor
	budget    Budget

	eventMu      sync.Mutex
	eventsClosed bool

	mu            sync.RWMutex
	sessions      map[string]AgentSession
	result        Result
	err           error
	usage         Usage
	backendResult *Completion
	progressValue Completion
	stagnatedFlag bool
	budgetErr     error
}

func newRun(runner *Runner, id string, ctx context.Context, cancel context.CancelFunc) *Run {
	return &Run{ID: id, runner: runner, ctx: ctx, cancel: cancel, events: make(chan Event, 256), done: make(chan struct{}), sessions: map[string]AgentSession{}}
}

func (r *Run) Events() <-chan Event  { return r.events }
func (r *Run) Done() <-chan struct{} { return r.done }

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

func (r *Run) Cancel(context.Context) error { r.cancel(); return nil }

func (r *Run) Steer(ctx context.Context, sessionID, message string) error {
	r.mu.RLock()
	session := r.sessions[sessionID]
	r.mu.RUnlock()
	if session == nil {
		return ErrRunNotFound
	}
	return session.Steer(ctx, message)
}

func (r *Run) addSession(id string, session AgentSession) {
	r.mu.Lock()
	r.sessions[id] = session
	r.mu.Unlock()
}
func (r *Run) removeSession(id string) { r.mu.Lock(); delete(r.sessions, id); r.mu.Unlock() }
func (r *Run) sessionWorkspace(sessionID string) string {
	return r.root + "/" + sessionID
}
func (r *Run) redactor() *Redactor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.redaction == nil {
		return NewRedactor()
	}
	return r.redaction
}
func (r *Run) addUsage(usage Usage) {
	r.mu.Lock()
	r.usage.InputTokens += usage.InputTokens
	r.usage.OutputTokens += usage.OutputTokens
	r.usage.TotalTokens += usage.TotalTokens
	r.usage.ModelCalls += usage.ModelCalls
	r.usage.ToolCalls += usage.ToolCalls
	r.usage.CostUSD += usage.CostUSD
	breach := budgetBreach(r.budget, r.usage)
	if breach != nil && r.budgetErr == nil {
		r.budgetErr = breach
	}
	r.mu.Unlock()
	if breach != nil {
		r.cancel()
	}
}

func (r *Run) budgetExceeded() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.budgetErr
}

// budgetBreach returns the first run-level budget the accumulated usage
// exceeds. A zero budget field means the dimension is unbounded.
func budgetBreach(budget Budget, used Usage) error {
	totalTokens := used.TotalTokens
	if totalTokens == 0 {
		totalTokens = used.InputTokens + used.OutputTokens
	}
	switch {
	case budget.MaxTokens > 0 && totalTokens > budget.MaxTokens:
		return fmt.Errorf("token budget exceeded: %d > %d", totalTokens, budget.MaxTokens)
	case budget.MaxModelCalls > 0 && used.ModelCalls > budget.MaxModelCalls:
		return fmt.Errorf("model call budget exceeded: %d > %d", used.ModelCalls, budget.MaxModelCalls)
	case budget.MaxToolCalls > 0 && used.ToolCalls > budget.MaxToolCalls:
		return fmt.Errorf("tool call budget exceeded: %d > %d", used.ToolCalls, budget.MaxToolCalls)
	case budget.MaxCostUSD > 0 && used.CostUSD > budget.MaxCostUSD:
		return fmt.Errorf("cost budget exceeded: %.4f > %.4f", used.CostUSD, budget.MaxCostUSD)
	}
	return nil
}
func (r *Run) progress() Completion { r.mu.RLock(); defer r.mu.RUnlock(); return r.progressValue }
func (r *Run) setProgress(completion Completion) {
	r.mu.Lock()
	r.progressValue = completion
	r.mu.Unlock()
}
func (r *Run) setBackendCompletion(completion Completion) {
	r.mu.Lock()
	if r.backendResult == nil {
		copy := completion
		r.backendResult = &copy
	}
	r.mu.Unlock()
}
func (r *Run) backendCompletion() *Completion {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.backendResult == nil {
		return nil
	}
	copy := *r.backendResult
	return &copy
}
func (r *Run) setControlError(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	if r.err == nil {
		r.err = err
	}
	r.mu.Unlock()
}
func (r *Run) setStagnated()   { r.mu.Lock(); r.stagnatedFlag = true; r.mu.Unlock() }
func (r *Run) stagnated() bool { r.mu.RLock(); defer r.mu.RUnlock(); return r.stagnatedFlag }
func (r *Run) sessionResults() []SessionResult {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.collectSessions(StatusRunning)
}

func (r *Run) collectSessions(status Status) []SessionResult {
	out := make([]SessionResult, 0, len(r.sessions))
	for id, session := range r.sessions {
		out = append(out, SessionResult{ID: id, Status: status, Usage: session.Usage()})
	}
	return out
}

func (r *Run) finishExecution(status Status, err error, verdict *Verdict, state *explorationState) {
	now := time.Now().UTC()
	redactor := r.redactor()
	errorText := redactError(redactor, err)
	var result Result
	if state != nil {
		result = Result{RunID: r.ID, Status: status, Verdict: verdict, Branches: append([]Branch(nil), state.branches...), Attempts: append([]Attempt(nil), state.attempts...), Observations: append([]Observation(nil), state.observations...), Knowledge: append([]Knowledge(nil), state.knowledge...), Artifacts: append([]Artifact(nil), state.artifacts...)}
	} else {
		result = Result{RunID: r.ID, Status: status, Verdict: verdict}
	}
	r.mu.RLock()
	result.Sessions = append([]SessionResult(nil), r.collectSessions(StatusSucceeded)...)
	result.Usage = r.usage
	result.StartedAt = r.startedAt
	r.mu.RUnlock()
	result.FinishedAt = &now
	result.Error = errorText
	if verdict != nil {
		verdict.Message = redactor.Redact(verdict.Message)
		verdict.Evidence = redactJSONPreserve(redactor, verdict.Evidence)
	}
	r.mu.RLock()
	controlErr := r.err
	r.mu.RUnlock()
	if err == nil && controlErr != nil {
		err = controlErr
		if status == StatusSucceeded {
			status = StatusFailed
			result.Status = StatusFailed
			errorText = redactError(redactor, controlErr)
			result.Error = errorText
		}
	}
	result = redactResult(redactor, result)
	record, getErr := r.runner.store.GetRun(context.Background(), r.ID)
	if getErr != nil {
		status = StatusFailed
		result.Status = StatusFailed
		result.Error = redactError(redactor, getErr)
		err = getErr
		record = RunRecord{ID: r.ID}
	}
	record.Status = status
	record.Result = mustJSON(result)
	record.Error = result.Error
	record.FinishedAt = &now
	var verdictRecord *VerdictRecord
	if verdict != nil {
		verdictRecord = &VerdictRecord{RunID: r.ID, Success: verdict.Success, Score: verdict.Score, Message: verdict.Message, Evidence: verdict.Evidence, CreatedAt: now}
	}
	// The Run record, its verdict and run.finalized are one canonical write.
	// A partial audit would show a finished Run with no verdict (or the
	// reverse), so the store commits them atomically or not at all.
	stored, finalizeErr := r.runner.store.Finalize(context.Background(), FinalizationRecord{Run: record, Verdict: verdictRecord, Event: Event{RunID: r.ID, Source: "harness", Type: "run.finalized", Time: now, Data: mustJSON(map[string]string{"status": string(status), "error": result.Error})}})
	if finalizeErr != nil {
		status = StatusFailed
		result.Status = StatusFailed
		result.Error = redactError(redactor, finalizeErr)
		err = finalizeErr
		record.Status = status
		record.Result = mustJSON(result)
		record.Error = result.Error
		// Best effort only: the transactional write already failed, and the
		// failed outcome must still be visible if the store can take it.
		_ = r.runner.store.UpdateRun(context.Background(), record)
	}
	// Deliver the stored finalization event and close the stream under one
	// lock, so no later writer can interleave an event after it.
	r.eventMu.Lock()
	if !r.eventsClosed {
		if finalizeErr == nil {
			select {
			case r.events <- stored:
			default:
			}
		}
		r.eventsClosed = true
		close(r.events)
	}
	r.eventMu.Unlock()
	r.mu.Lock()
	r.result = result
	r.err = err
	close(r.done)
	r.mu.Unlock()
}
