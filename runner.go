package harness

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Runner owns Run lifecycle and adapter composition.
type Runner struct {
	store         Store
	starter       ProcessStarter
	workspaceRoot string
	maxSessions   int
	pollInterval  time.Duration
	closeTimeout  time.Duration
	cleaner       ResourceCleaner
	logger        Logger

	mu   sync.Mutex
	runs map[string]*Run
}

// NewRunner validates configuration and marks unfinished historical runs as
// interrupted unless recovery was explicitly skipped.
func NewRunner(cfg Config) (*Runner, error) {
	if cfg.Store == nil {
		return nil, errors.New("runner store is required")
	}
	if cfg.MaxSessions < 0 || cfg.PollInterval < 0 || cfg.CloseTimeout < 0 {
		return nil, errors.New("runner durations and MaxSessions must not be negative")
	}
	if cfg.MaxSessions == 0 {
		cfg.MaxSessions = 4
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	if cfg.CloseTimeout == 0 {
		cfg.CloseTimeout = 10 * time.Second
	}
	if cfg.WorkspaceRoot == "" {
		cfg.WorkspaceRoot = ".harness/workspaces"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	}
	runner := &Runner{store: cfg.Store, workspaceRoot: cfg.WorkspaceRoot, maxSessions: cfg.MaxSessions, pollInterval: cfg.PollInterval, closeTimeout: cfg.CloseTimeout, cleaner: cfg.Cleaner, logger: cfg.Logger, runs: map[string]*Run{}}
	if !cfg.SkipRecovery {
		if err := cfg.Store.MarkInterrupted(context.Background()); err != nil {
			return nil, fmt.Errorf("mark interrupted runs: %w", err)
		}
		if cfg.Cleaner != nil {
			if err := cfg.Cleaner.CleanupOrphans(context.Background()); err != nil {
				return nil, fmt.Errorf("cleanup orphans: %w", err)
			}
		}
	}
	return runner, nil
}

// Start validates the RunSpec and starts one asynchronous Run.
func (r *Runner) Start(ctx context.Context, spec RunSpec) (*Run, error) {
	if err := ValidateRunSpec(spec); err != nil {
		return nil, err
	}
	runID, err := randomID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	runCtx, cancel := context.WithCancel(context.Background())
	run := newRun(r, runID, runCtx, cancel)
	run.redaction = redactorForSpec(spec)
	run.startedAt = &now
	descriptor := RunDescriptor{SchemaVersion: 1, Task: spec.Task, Target: spec.Target, Planner: spec.Planner, Executors: spec.Executors, Policy: NormalizeExplorationPolicy(spec.Policy), Budget: spec.Budget}
	specJSON, _ := json.Marshal(descriptor)
	if err := r.store.CreateRun(ctx, RunRecord{ID: runID, Status: StatusQueued, Spec: specJSON, CreatedAt: now}); err != nil {
		return nil, fmt.Errorf("create run: %w", err)
	}
	r.mu.Lock()
	r.runs[runID] = run
	r.mu.Unlock()
	if err := r.emitEvent(run, redactorForSpec(spec), Event{RunID: runID, Source: "harness", Type: "run.created", Time: now, Data: mustJSON(map[string]string{"task_id": spec.Task.ID, "target_id": spec.Target.ID})}); err != nil {
		cancel()
		r.mu.Lock()
		delete(r.runs, runID)
		r.mu.Unlock()
		return nil, err
	}
	go r.execute(run, spec, redactorForSpec(spec), descriptor)
	return run, nil
}

// Run starts a Run and waits for its result.
func (r *Runner) Run(ctx context.Context, spec RunSpec) (Result, error) {
	run, err := r.Start(ctx, spec)
	if err != nil {
		return Result{}, err
	}
	return run.Wait(ctx)
}

func (r *Runner) execute(run *Run, spec RunSpec, redactor *Redactor, descriptor RunDescriptor) {
	var state *explorationState
	var terminalStatus = StatusFailed
	var finalErr error
	var verdict *Verdict
	started := time.Now().UTC()
	run.startedAt = &started
	workspaceRoot := spec.WorkspaceRoot
	if workspaceRoot == "" {
		workspaceRoot = r.workspaceRoot
	}
	run.root = filepath.Join(workspaceRoot, run.ID)
	run.taskDir = filepath.Join(run.root, "task")
	run.skillsDir = filepath.Join(run.root, "skills")
	if err := ensureDir(run.taskDir); err != nil {
		finalErr = err
		run.finishExecution(terminalStatus, finalErr, verdict, state)
		return
	}
	if err := ensureDir(run.skillsDir); err != nil {
		finalErr = err
		run.finishExecution(terminalStatus, finalErr, verdict, state)
		return
	}
	if err := r.updateStatus(run, StatusProvisioning, nil); err != nil {
		finalErr = err
		run.finishExecution(terminalStatus, finalErr, verdict, state)
		return
	}
	target := spec.Target
	if spec.Backend != nil {
		provisioned, err := spec.Backend.Provision(run.ctx, ProvisionRequest{RunID: run.ID, Task: spec.Task, Target: spec.Target})
		if err != nil {
			finalErr = err
			run.finishExecution(terminalStatus, finalErr, verdict, state)
			return
		}
		if provisioned.ID == "" {
			provisioned.ID = spec.Target.ID
		}
		if provisioned.Isolation == "" {
			provisioned.Isolation = IsolationCallerManaged
		}
		if provisioned.Isolation != IsolationCallerManaged {
			finalErr = fmt.Errorf("backend target isolation %q is unsupported", provisioned.Isolation)
			run.finishExecution(terminalStatus, finalErr, verdict, state)
			return
		}
		target = provisioned
	}
	run.setTarget(target)
	if err := r.emitEvent(run, redactor, Event{RunID: run.ID, Source: "harness", Type: "run.provisioned", Time: time.Now().UTC(), Data: mustJSON(target)}); err != nil {
		finalErr = err
		run.finishExecution(terminalStatus, finalErr, verdict, state)
		return
	}
	if spec.Backend != nil {
		go r.watchBackend(run, spec.Backend, target, redactor)
	}
	if err := r.updateStatus(run, StatusRunning, nil); err != nil {
		finalErr = err
		run.finishExecution(terminalStatus, finalErr, verdict, state)
		return
	}
	state = newExplorationState(run.ID, spec)
	state.target = target
	engine := &explorationEngine{runner: r, live: run, spec: spec, state: state}
	err := engine.run(run.ctx)
	if completion := run.backendCompletion(); completion != nil {
		verdict = &Verdict{Success: completion.Success, Score: completion.Score, Message: completion.Message, Evidence: mustJSON(completion.Metadata)}
		if completion.Success {
			terminalStatus = StatusSucceeded
		} else {
			terminalStatus = StatusFailed
		}
	} else if run.ctx.Err() != nil {
		if run.stagnated() {
			terminalStatus = StatusStagnated
		} else if deadlineExceeded(run.ctx) {
			terminalStatus = StatusTimedOut
		} else {
			terminalStatus = StatusCancelled
		}
		finalErr = run.ctx.Err()
	} else if err != nil {
		finalErr = err
	} else if spec.Verifier != nil {
		if updateErr := r.updateStatus(run, StatusFinalizing, nil); updateErr != nil {
			finalErr = updateErr
		} else if updateErr := r.updateStatus(run, StatusVerifying, nil); updateErr != nil {
			finalErr = updateErr
		} else {
			verifyVerdict, verifyErr := spec.Verifier.Verify(run.ctx, VerifyRequest{RunID: run.ID, Task: spec.Task, Target: target, Sessions: run.sessionResults(), WorkspaceRoot: run.root, Store: r.store})
			if verifyErr != nil {
				finalErr = verifyErr
			} else {
				verdict = &verifyVerdict
				if verifyVerdict.Success {
					terminalStatus = StatusSucceeded
				} else {
					terminalStatus = StatusFailed
				}
			}
		}
	} else if run.stagnated() {
		terminalStatus = StatusStagnated
	} else {
		finalErr = errors.New("run has no authoritative verdict")
	}
	if spec.Backend != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), r.closeTimeout)
		if closeErr := spec.Backend.Close(closeCtx, target); closeErr != nil && terminalStatus == StatusSucceeded {
			terminalStatus = StatusFailed
			finalErr = fmt.Errorf("close backend: %w", closeErr)
		}
		cancel()
	}
	run.finishExecution(terminalStatus, finalErr, verdict, state)
	r.mu.Lock()
	delete(r.runs, run.ID)
	r.mu.Unlock()
}

func (r *Runner) watchBackend(run *Run, backend Backend, target Target, redactor *Redactor) {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-run.ctx.Done():
			return
		case <-ticker.C:
		}
		completion, err := backend.Observe(run.ctx, target)
		if err != nil {
			if emitErr := r.emitEvent(run, redactor, Event{RunID: run.ID, Source: "backend", Type: "backend.observe_error", Time: time.Now().UTC(), Data: mustJSON(map[string]string{"error": redactor.Redact(err.Error())})}); emitErr != nil {
				run.setControlError(emitErr)
				run.cancel()
				return
			}
			continue
		}
		run.setProgress(completion)
		if completion.Done {
			run.setBackendCompletion(completion)
			_ = r.emitEvent(run, redactor, Event{RunID: run.ID, Source: "backend", Type: "backend.completed", Time: time.Now().UTC(), Data: mustJSON(completion)})
			run.cancel()
			return
		}
	}
}

func (r *Runner) updateStatus(run *Run, status Status, err error) error {
	record, getErr := r.store.GetRun(context.Background(), run.ID)
	if getErr != nil {
		return getErr
	}
	record.Status = status
	if err != nil {
		record.Error = run.redactor().Redact(err.Error())
	}
	now := time.Now().UTC()
	if status == StatusProvisioning {
		record.StartedAt = &now
	}
	if isTerminal(status) {
		record.FinishedAt = &now
	}
	if err := r.store.UpdateRun(context.Background(), record); err != nil {
		return fmt.Errorf("persist run status %s: %w", status, err)
	}
	return r.emitEvent(run, run.redactor(), Event{RunID: run.ID, Source: "harness", Type: "run.status", Time: now, Data: mustJSON(map[string]string{"status": string(status)})})
}

func (r *Runner) emitEvent(run *Run, redactor *Redactor, event Event) error {
	if event.SchemaVersion == 0 {
		event.SchemaVersion = 1
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	if redactor != nil {
		event.Data = redactJSONPreserve(redactor, event.Data)
		event.Raw = redactJSONPreserve(redactor, event.Raw)
	}
	run.eventMu.Lock()
	defer run.eventMu.Unlock()
	if run.eventsClosed {
		return errors.New("run event stream closed")
	}
	stored, err := r.store.AppendEvent(context.Background(), event)
	if err != nil {
		run.setControlError(err)
		return fmt.Errorf("persist event %q: %w", event.Type, err)
	}
	select {
	case run.events <- stored:
	default:
	}
	return nil
}

func (r *Runner) appendEventChecked(run *Run, redactor *Redactor, event Event) error {
	return r.emitEvent(run, redactor, event)
}

func (r *Runner) appendLog(run *Run, redactor *Redactor, record LogRecord) error {
	if record.Time.IsZero() {
		record.Time = time.Now().UTC()
	}
	if redactor != nil {
		record.Text = redactor.Redact(record.Text)
	}
	if _, err := r.store.AppendLog(context.Background(), record); err != nil {
		run.setControlError(err)
		run.cancel()
		return fmt.Errorf("persist log: %w", err)
	}
	return nil
}

func (r *Runner) persistBranch(ctx context.Context, branch Branch) error {
	return r.store.SaveBranch(ctx, branch)
}
func (r *Runner) persistAttempt(ctx context.Context, attempt Attempt) error {
	return r.store.SaveAttempt(ctx, attempt)
}
func (r *Runner) persistObservation(ctx context.Context, observation Observation) error {
	return r.store.SaveObservation(ctx, observation)
}
func (r *Runner) persistArtifact(ctx context.Context, artifact Artifact) error {
	return r.store.SaveArtifact(ctx, artifact)
}
func (r *Runner) persistKnowledge(ctx context.Context, knowledge Knowledge) error {
	if knowledge.Version <= 1 {
		return r.store.SaveKnowledge(ctx, knowledge)
	}
	return r.store.UpdateKnowledge(ctx, knowledge)
}

func (r *Runner) queryReusableKnowledge(ctx context.Context, spec RunSpec) ([]Knowledge, error) {
	fingerprint := targetFingerprint(spec.Target)
	items, err := r.store.QueryKnowledge(ctx, KnowledgeQuery{TaskID: spec.Task.ID, RunID: "", TargetFingerprint: fingerprint, AllowCrossRun: true, Now: time.Now().UTC()})
	if err != nil {
		return nil, err
	}
	return FilterKnowledge(items, KnowledgeQuery{TaskID: spec.Task.ID, TargetFingerprint: fingerprint, AllowCrossRun: true, Now: time.Now().UTC()}), nil
}

func (r *Runner) Close(ctx context.Context) error {
	r.mu.Lock()
	runs := make([]*Run, 0, len(r.runs))
	for _, run := range r.runs {
		runs = append(runs, run)
	}
	r.mu.Unlock()
	for _, run := range runs {
		_ = run.Cancel(ctx)
	}
	return r.store.Close()
}

func ValidateRunSpec(spec RunSpec) error {
	if strings.TrimSpace(spec.Task.ID) == "" {
		return errors.New("task.id is required")
	}
	if strings.TrimSpace(spec.Task.Objective) == "" {
		return errors.New("task.objective is required")
	}
	if strings.TrimSpace(spec.Target.ID) == "" {
		return errors.New("target.id is required")
	}
	isolation := spec.Target.Isolation
	if isolation == "" {
		isolation = IsolationCallerManaged
	}
	if isolation != IsolationCallerManaged {
		return fmt.Errorf("target.isolation %q is unsupported", isolation)
	}
	if spec.Planner.Runtime == nil {
		return errors.New("planner.runtime is required")
	}
	if len(spec.Executors) == 0 {
		return errors.New("executors are required")
	}
	seen := map[string]struct{}{}
	for i, executor := range spec.Executors {
		if strings.TrimSpace(executor.ID) == "" {
			return fmt.Errorf("executors[%d].id is required", i)
		}
		if _, ok := seen[executor.ID]; ok {
			return fmt.Errorf("executors[%d].id is duplicated", i)
		}
		seen[executor.ID] = struct{}{}
		if executor.Runtime == nil {
			return fmt.Errorf("executors[%d].runtime is required", i)
		}
	}
	if spec.Artifacts == nil {
		return errors.New("artifacts store is required")
	}
	if spec.Verifier == nil && spec.Backend == nil {
		return errors.New("authority is required: verifier or backend")
	}
	return nil
}

func isTerminal(status Status) bool {
	switch status {
	case StatusSucceeded, StatusFailed, StatusCancelled, StatusTimedOut, StatusInterrupted, StatusStagnated:
		return true
	}
	return false
}

func deadlineExceeded(ctx context.Context) bool {
	return errors.Is(ctx.Err(), context.DeadlineExceeded)
}

func randomID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func ensureDir(path string) error { return os.MkdirAll(path, 0o700) }

func buildEnvironment(runID, sessionID string, spec RunSpec, agent AgentSpec, target Target, workspaceDir, taskDir, skillsDir string) map[string]string {
	env := map[string]string{"HARNESS_RUN_ID": runID, "HARNESS_SESSION_ID": sessionID, "HARNESS_TARGET_ID": target.ID, "HARNESS_WORKSPACE": workspaceDir, "HARNESS_TASK_DIR": taskDir, "HARNESS_SKILLS_DIR": skillsDir, "HOME": "/workspace"}
	for key, value := range agent.Env {
		env[key] = value
	}
	for _, key := range agent.EnvFrom {
		if value, ok := os.LookupEnv(key); ok {
			env[key] = value
		}
	}
	for _, key := range spec.SecretEnv {
		if value, ok := os.LookupEnv(key); ok {
			env[key] = value
		}
	}
	return env
}

func mustJSON(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
func redactError(r *Redactor, err error) string {
	if err == nil {
		return ""
	}
	if r == nil {
		return err.Error()
	}
	return r.Redact(err.Error())
}
