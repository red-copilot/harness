package harness

import (
	"context"
	"errors"
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

	eventMu      sync.Mutex
	eventsClosed bool

	mu            sync.RWMutex
	sessions      map[string]AgentSession
	result        Result
	err           error
	usage         Usage
	backendDone   chan Completion
	backendResult *Completion
	progressValue Completion
	stagnatedFlag bool
}

func newRun(runner *Runner, id string, ctx context.Context, cancel context.CancelFunc) *Run {
	return &Run{ID: id, runner: runner, ctx: ctx, cancel: cancel, events: make(chan Event, 256), done: make(chan struct{}), sessions: map[string]AgentSession{}, backendDone: make(chan Completion, 1)}
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
func (r *Run) setTarget(target Target) {
	r.mu.Lock()
	r.progressValue.Metadata = target.Metadata
	r.mu.Unlock()
}
func (r *Run) sessionWorkspace(runID, sessionID string) string {
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
	r.mu.Unlock()
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
	out := make([]SessionResult, 0, len(r.sessions))
	for id, session := range r.sessions {
		out = append(out, SessionResult{ID: id, Status: StatusRunning, Usage: session.Usage()})
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
	result.Sessions = append([]SessionResult(nil), r.sessionResultsUnlocked()...)
	result.Usage = r.usage
	result.StartedAt = r.startedAt
	r.mu.RUnlock()
	result.FinishedAt = &now
	result.Error = errorText
	if verdict != nil {
		verdict.Message = redactor.Redact(verdict.Message)
		verdict.Evidence = redactJSONPreserve(redactor, verdict.Evidence)
	}
	if verdict != nil {
		_ = r.runner.store.SaveVerdict(context.Background(), VerdictRecord{RunID: r.ID, Success: verdict.Success, Score: verdict.Score, Message: verdict.Message, Evidence: verdict.Evidence, CreatedAt: now})
	}
	result = redactResult(redactor, result)
	resultJSON, _ := jsonMarshal(result)
	record, getErr := r.runner.store.GetRun(context.Background(), r.ID)
	if getErr == nil {
		record.Status = status
		record.Result = resultJSON
		record.Error = errorText
		record.FinishedAt = &now
		if updateErr := r.runner.store.UpdateRun(context.Background(), record); updateErr != nil {
			status = StatusFailed
			result.Status = StatusFailed
			result.Error = redactor.Redact(updateErr.Error())
			err = updateErr
		}
	} else if status == StatusSucceeded {
		status = StatusFailed
		result.Status = StatusFailed
		result.Error = redactor.Redact(getErr.Error())
		err = getErr
	}
	_ = r.runner.emitEvent(r, redactor, Event{RunID: r.ID, Source: "harness", Type: "run.finalized", Time: now, Data: mustJSON(map[string]string{"status": string(status), "error": result.Error})})
	r.eventMu.Lock()
	if !r.eventsClosed {
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

func (r *Run) sessionResultsUnlocked() []SessionResult {
	out := make([]SessionResult, 0, len(r.sessions))
	for id, session := range r.sessions {
		out = append(out, SessionResult{ID: id, Status: StatusSucceeded, Usage: session.Usage()})
	}
	return out
}
