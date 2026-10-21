package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type workflowSession struct {
	lease   SessionLease
	logDone chan struct{}
	logErr  error
}

// workflowRuntime is the run-scoped capability surface given to a Workflow.
// It is the only path from workflow orchestration to session and audit state.
type workflowRuntime struct {
	runner      *Runner
	run         *Run
	spec        RunSpec
	redactor    *Redactor
	target      Target
	runRoot     string
	taskDir     string
	skillsDir   string
	progress    *completionTracker
	maxSessions int

	mu       sync.Mutex
	starting map[string]struct{}
	sessions map[string]*workflowSession
	order    []string
	closed   bool
}

func newWorkflowRuntime(
	runner *Runner,
	run *Run,
	spec RunSpec,
	redactor *Redactor,
	target Target,
	runRoot string,
	taskDir string,
	skillsDir string,
	progress *completionTracker,
) *workflowRuntime {
	return &workflowRuntime{
		runner: runner, run: run, spec: spec, redactor: redactor, target: target,
		runRoot: runRoot, taskDir: taskDir, skillsDir: skillsDir, progress: progress,
		maxSessions: runner.maxSessions, starting: map[string]struct{}{}, sessions: map[string]*workflowSession{},
	}
}

func (rt *workflowRuntime) RunID() string { return rt.run.ID }

func (rt *workflowRuntime) Task() Task { return rt.spec.Task }

func (rt *workflowRuntime) Target() Target { return rt.target }

func (rt *workflowRuntime) Agents() []AgentSpec {
	return append([]AgentSpec(nil), rt.spec.Agents...)
}

func (rt *workflowRuntime) Budget() Budget { return rt.spec.Budget }

func (rt *workflowRuntime) WorkspaceRoot() string { return rt.runRoot }

func (rt *workflowRuntime) Progress() Completion {
	if rt.progress == nil {
		return Completion{}
	}
	return rt.progress.Snapshot()
}

func (rt *workflowRuntime) Redact(value string) string { return rt.redactor.Redact(value) }

func (rt *workflowRuntime) OpenSession(ctx context.Context, agent AgentSpec) (SessionLease, error) {
	sessionID := strings.TrimSpace(agent.ID)
	if sessionID == "" {
		return SessionLease{}, errors.New("session id is required")
	}

	rt.mu.Lock()
	if rt.closed {
		rt.mu.Unlock()
		return SessionLease{}, errors.New("workflow runtime is closed")
	}
	if _, ok := rt.sessions[sessionID]; ok {
		rt.mu.Unlock()
		return SessionLease{}, fmt.Errorf("session %q is already open", sessionID)
	}
	if _, ok := rt.starting[sessionID]; ok {
		rt.mu.Unlock()
		return SessionLease{}, fmt.Errorf("session %q is already starting", sessionID)
	}
	if len(rt.sessions)+len(rt.starting) >= rt.maxSessions {
		rt.mu.Unlock()
		return SessionLease{}, fmt.Errorf("session maximum %d reached", rt.maxSessions)
	}
	rt.starting[sessionID] = struct{}{}
	rt.mu.Unlock()

	defer func() {
		rt.mu.Lock()
		delete(rt.starting, sessionID)
		rt.mu.Unlock()
	}()

	workspaceDir := sessionWorkspace(rt.runRoot, sessionID)
	if err := os.MkdirAll(workspaceDir, 0o700); err != nil {
		return SessionLease{}, fmt.Errorf("create session workspace: %w", err)
	}
	factory := agent.Factory
	if factory == nil {
		factory = rt.runner.defaultFactory
	}
	if factory == nil {
		return SessionLease{}, fmt.Errorf("no session factory configured for %s", sessionID)
	}
	env := buildEnvironment(rt.run.ID, sessionID, rt.spec, agent, rt.target, workspaceDir, rt.taskDir, rt.skillsDir)
	session, err := factory.NewSession(ctx, SessionRequest{
		RunID: rt.run.ID, SessionID: sessionID, Agent: agent, Task: rt.spec.Task, Target: rt.target,
		WorkspaceDir: workspaceDir, TaskDir: rt.taskDir, SkillsDir: rt.skillsDir, Env: env, Starter: rt.runner.starter,
	})
	if err != nil {
		return SessionLease{}, err
	}

	handle := &workflowSession{
		lease:   SessionLease{ID: sessionID, Agent: agent, WorkspaceDir: workspaceDir, Session: session},
		logDone: make(chan struct{}),
	}
	rt.mu.Lock()
	if rt.closed {
		rt.mu.Unlock()
		_ = session.Abort(context.Background())
		_ = session.Close(context.Background())
		return SessionLease{}, errors.New("workflow runtime closed while opening session")
	}
	rt.sessions[sessionID] = handle
	rt.order = append(rt.order, sessionID)
	rt.mu.Unlock()

	rt.run.addSession(sessionID, session)
	if err := rt.Emit(ctx, Event{
		SessionID: sessionID, Source: "harness", Type: "session.started",
	}); err != nil {
		return SessionLease{}, err
	}
	if source, ok := session.(LogSource); ok {
		go func() {
			defer close(handle.logDone)
			for {
				select {
				case log, ok := <-source.Logs():
					if !ok {
						return
					}
					if err := rt.runner.appendLog(rt.run, rt.redactor, LogRecord{
						RunID: rt.run.ID, SessionID: sessionID, Stream: log.Stream, Time: time.Now().UTC(), Text: log.Text,
					}); err != nil {
						handle.logErr = err
						rt.run.setControlError(err)
						rt.run.cancel()
						return
					}
				case <-rt.run.ctx.Done():
					return
				}
			}
		}()
	} else {
		close(handle.logDone)
	}
	return handle.lease, nil
}

func (rt *workflowRuntime) Emit(ctx context.Context, event Event) error {
	rt.mu.Lock()
	handle := rt.sessions[event.SessionID]
	rt.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if event.RunID == "" {
		event.RunID = rt.run.ID
	}
	if event.RunID != rt.run.ID {
		return fmt.Errorf("event run id %q does not match %q", event.RunID, rt.run.ID)
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	if _, err := rt.runner.appendEvent(rt.run, rt.redactor, event); err != nil {
		return err
	}
	if handle != nil {
		rt.runner.checkLiveBudget(rt.run, rt.spec.Budget, handle.lease.Session)
	}
	return nil
}

// CloseSessions aborts and closes every opened session exactly once, waits for
// log pumps, records session results, and returns them in open order.
func (rt *workflowRuntime) CloseSessions(ctx context.Context, workflowErr error) ([]SessionResult, error) {
	rt.mu.Lock()
	rt.closed = true
	order := append([]string(nil), rt.order...)
	handles := make([]*workflowSession, 0, len(order))
	for _, id := range order {
		handles = append(handles, rt.sessions[id])
	}
	rt.mu.Unlock()

	results := make([]SessionResult, 0, len(handles))
	var closeErrors []error
	for _, handle := range handles {
		_ = handle.lease.Session.Abort(context.Background())
		if err := handle.lease.Session.Close(context.Background()); err != nil {
			closeErrors = append(closeErrors, fmt.Errorf("close session %s: %w", handle.lease.ID, err))
		}
		select {
		case <-handle.logDone:
		case <-time.After(2 * time.Second):
			closeErrors = append(closeErrors, fmt.Errorf("timed out waiting for %s logs", handle.lease.ID))
		}

		result := SessionResult{ID: handle.lease.ID, Status: StatusSucceeded}
		if source, ok := handle.lease.Session.(UsageSource); ok {
			result.Usage = source.Usage()
		}
		switch {
		case ctx.Err() != nil:
			result.Status = StatusCancelled
			result.Error = rt.redactor.Redact(ctx.Err().Error())
		case workflowErr != nil:
			result.Status = StatusFailed
			result.Error = rt.redactor.Redact(workflowErr.Error())
		case handle.logErr != nil:
			result.Status = StatusFailed
			result.Error = rt.redactor.Redact(handle.logErr.Error())
		}
		if err := rt.Emit(context.Background(), Event{
			SessionID: handle.lease.ID, Source: "harness", Type: "session.finished", Data: mustJSON(result),
		}); err != nil {
			closeErrors = append(closeErrors, err)
		}
		if err := rt.Emit(context.Background(), Event{
			SessionID: handle.lease.ID, Source: "harness", Type: "session.closed",
		}); err != nil {
			closeErrors = append(closeErrors, err)
		}
		if handle.logErr != nil {
			closeErrors = append(closeErrors, handle.logErr)
		}
		results = append(results, result)
	}
	return results, errors.Join(closeErrors...)
}

type workflowOutcome struct {
	result WorkflowResult
	err    error
}

// executeWorkflow is the minimal run path used by injected workflows. Runner
// remains the sole owner of backend authority, verdicts, persistence and cleanup.
func (r *Runner) executeWorkflow(run *Run, spec RunSpec, redactor *Redactor) {
	started := time.Now().UTC()
	if err := r.updateRun(run, func(record *RunRecord) {
		record.Status = StatusRunning
		record.StartedAt = &started
	}); err != nil {
		r.fail(run, redactor, started, err)
		return
	}
	if _, err := r.appendEvent(run, redactor, Event{
		RunID: run.ID, Source: "harness", Type: "run.started", Time: started,
	}); err != nil {
		r.fail(run, redactor, started, err)
		return
	}

	target := spec.Target
	progress := &completionTracker{}
	closeBackend := func() {}
	defer func() { closeBackend() }()
	if spec.Backend != nil {
		var err error
		target, err = spec.Backend.Provision(run.ctx, ProvisionRequest{
			RunID: run.ID, Task: spec.Task, Metadata: target.Metadata,
		})
		if err != nil {
			r.fail(run, redactor, started, err)
			return
		}
		closeBackend = func() {
			if err := spec.Backend.Close(context.Background(), target); err != nil {
				r.logger.Warn("backend close failed", "run_id", run.ID, "error", err)
			}
		}
		if _, err := r.appendEvent(run, redactor, Event{
			RunID: run.ID, Source: "backend", Type: "backend.provisioned", Time: time.Now().UTC(), Data: mustJSON(target),
		}); err != nil {
			r.fail(run, redactor, started, err)
			return
		}
		if completion, err := spec.Backend.Observe(run.ctx, target); err == nil {
			r.recordBackendProgress(run, redactor, progress, completion)
		}
	}

	runRoot := filepath.Join(r.workspaceRoot, run.ID)
	if spec.WorkspaceRoot != "" {
		runRoot = spec.WorkspaceRoot
	}
	taskDir := filepath.Join(runRoot, "task")
	skillsDir := filepath.Join(runRoot, "skills")
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		r.fail(run, redactor, started, fmt.Errorf("create task dir: %w", err))
		return
	}
	if err := os.MkdirAll(skillsDir, 0o700); err != nil {
		r.fail(run, redactor, started, fmt.Errorf("create skills dir: %w", err))
		return
	}
	if err := prepareEmbeddedSkills(skillsDir); err != nil {
		r.fail(run, redactor, started, fmt.Errorf("prepare embedded skills: %w", err))
		return
	}

	rt := newWorkflowRuntime(r, run, spec, redactor, target, runRoot, taskDir, skillsDir, progress)
	workflowOut := make(chan workflowOutcome, 1)
	go func() {
		result, err := spec.Workflow.Execute(run.ctx, rt)
		workflowOut <- workflowOutcome{result: result, err: err}
	}()

	var completionCh <-chan Completion
	var backgroundWG sync.WaitGroup
	if spec.Backend != nil {
		out := make(chan Completion, 1)
		completionCh = out
		backgroundWG.Add(1)
		go func() {
			defer backgroundWG.Done()
			r.observeBackend(run.ctx, redactor, run, spec.Backend, target, out, func(completion Completion) {
				r.recordBackendProgress(run, redactor, progress, completion)
			})
		}()
	}

	var (
		status         Status
		verdict        *Verdict
		runErr         error
		workflowResult WorkflowResult
		workflowErr    error
		workflowDone   bool
		pendingVerify  bool
	)

	setContextStatus := func() {
		if reason := run.controlError(); reason != "" {
			status, runErr = StatusFailed, errors.New(reason)
		} else if reason := run.budgetExceeded(); reason != "" {
			status, runErr = StatusFailed, fmt.Errorf("budget exceeded: %s", reason)
		} else if errors.Is(run.ctx.Err(), context.DeadlineExceeded) {
			status, runErr = StatusTimedOut, nil
		} else {
			status, runErr = StatusCancelled, nil
		}
	}

	handleCompletion := func(completion Completion) {
		verdict = completionToVerdict(completion)
		if verdict.Success {
			status = StatusSucceeded
		} else {
			status = StatusFailed
		}
		run.cancel()
	}

waitOutcome:
	for status == "" {
		select {
		case outcome := <-workflowOut:
			workflowDone = true
			workflowResult = outcome.result
			workflowErr = outcome.err
			if run.ctx.Err() != nil {
				setContextStatus()
				break waitOutcome
			}
			if outcome.err != nil {
				status, runErr = StatusFailed, outcome.err
				break waitOutcome
			}
			if outcome.result.Stagnated && spec.Verifier == nil {
				status = StatusStagnated
				break waitOutcome
			}
			if spec.Verifier != nil {
				pendingVerify = true
				break waitOutcome
			}
			if spec.Backend != nil {
				select {
				case completion := <-completionCh:
					handleCompletion(completion)
				case <-run.ctx.Done():
					setContextStatus()
				}
				break waitOutcome
			}
			status, runErr = StatusFailed, errors.New("run has no authoritative verdict source")
		case completion := <-completionCh:
			handleCompletion(completion)
		case <-run.ctx.Done():
			setContextStatus()
		}
	}

	if !workflowDone {
		run.cancel()
	}
	sessionResults, closeErr := rt.CloseSessions(run.ctx, workflowErr)
	if closeErr != nil && verdict == nil {
		status = StatusFailed
		runErr = errors.Join(runErr, closeErr)
	}
	if !workflowDone {
		select {
		case outcome := <-workflowOut:
			if workflowErr == nil {
				workflowErr = outcome.err
			}
			workflowResult = outcome.result
		case <-time.After(5 * time.Second):
			runErr = errors.Join(runErr, errors.New("workflow did not stop after cancellation"))
		}
	}

	if pendingVerify && status == "" && closeErr == nil {
		if reason := exceededBudget(aggregateUsage(sessionResults), spec.Budget); reason != "" {
			status, runErr = StatusFailed, fmt.Errorf("budget exceeded: %s", reason)
		} else {
			v, err := spec.Verifier.Verify(context.Background(), VerifyRequest{
				RunID: run.ID, Task: spec.Task, Target: target, Sessions: sessionResults,
				WorkspaceRoot: runRoot, Store: r.store,
			})
			if err != nil {
				status, runErr = StatusFailed, fmt.Errorf("verifier: %w", err)
			} else {
				verdict = &v
				if v.Success {
					status = StatusSucceeded
				} else {
					status = StatusFailed
				}
			}
		}
	}
	if status == "" {
		if workflowResult.Stagnated {
			status = StatusStagnated
		} else {
			status, runErr = StatusFailed, errors.New("run has no authoritative verdict")
		}
	}
	backgroundWG.Wait()

	if verdict != nil {
		redactedVerdict := *verdict
		redactedVerdict.Message = redactor.Redact(redactedVerdict.Message)
		redactedVerdict.Evidence = redactor.RedactJSON(redactedVerdict.Evidence)
		verdict = &redactedVerdict
		if err := r.store.SaveVerdict(context.Background(), VerdictRecord{
			RunID: run.ID, Success: verdict.Success, Score: verdict.Score,
			Message: verdict.Message, Evidence: verdict.Evidence, CreatedAt: time.Now().UTC(),
		}); err != nil {
			status = StatusFailed
			runErr = errors.Join(runErr, fmt.Errorf("persist verdict: %w", err))
			verdict = nil
		}
	}

	finished := time.Now().UTC()
	result := Result{
		RunID: run.ID, Status: status, Verdict: verdict, Sessions: sessionResults,
		Usage: aggregateUsage(sessionResults), StartedAt: &started, FinishedAt: &finished,
	}
	if runErr != nil {
		result.Error = redactor.Redact(runErr.Error())
		runErr = errors.New(result.Error)
	}
	if _, err := r.appendEvent(run, redactor, Event{
		RunID: run.ID, Source: "harness", Type: "run.finished", Time: finished,
		Data: mustJSON(map[string]any{"status": status, "verdict": verdict}),
	}); err != nil {
		status = StatusFailed
		result.Status = status
		result.Verdict = nil
		runErr = errors.Join(runErr, err)
		result.Error = redactor.Redact(runErr.Error())
	}
	resultJSON, _ := json.Marshal(result)
	resultJSON = redactor.RedactJSON(resultJSON)
	if err := r.updateRun(run, func(record *RunRecord) {
		record.Status = result.Status
		record.Result = resultJSON
		record.Error = result.Error
		record.FinishedAt = &finished
	}); err != nil {
		result.Status = StatusFailed
		result.Verdict = nil
		runErr = errors.Join(runErr, err)
		result.Error = redactor.Redact(runErr.Error())
	}
	run.finish(result, runErr)
}
