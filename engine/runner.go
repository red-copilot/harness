package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"
)

// Runner owns Run lifecycle and adapter composition.
type Runner struct {
	store         Store
	redacting     *redactingStore
	clock         Clock
	ids           IDSource
	secrets       SecretSource
	workspaceRoot string
	maxSessions   int
	pollInterval  time.Duration
	closeTimeout  time.Duration
	sessionSlots  chan struct{}
	closed        chan struct{}
	closeOnce     sync.Once

	mu   sync.Mutex
	runs map[string]*Run
}

// NewRunner validates configuration and marks unfinished historical runs as
// interrupted unless recovery was explicitly skipped.
func NewRunner(cfg Config) (*Runner, error) {
	if cfg.Store == nil {
		return nil, errors.New("runner store is required")
	}
	if cfg.Clock == nil {
		return nil, errors.New("runner clock is required")
	}
	if cfg.IDs == nil {
		return nil, errors.New("runner id source is required")
	}
	if cfg.Secrets == nil {
		return nil, errors.New("runner secret source is required")
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
	// The composition root installs the redacting boundary, so no Run and no
	// adapter can reach the canonical store without redaction.
	redacting := newRedactingStore(cfg.Store)
	runner := &Runner{
		store: redacting, redacting: redacting, clock: cfg.Clock, ids: cfg.IDs, secrets: cfg.Secrets,
		workspaceRoot: cfg.WorkspaceRoot,
		maxSessions:   cfg.MaxSessions, pollInterval: cfg.PollInterval, closeTimeout: cfg.CloseTimeout,
		runs: map[string]*Run{}, sessionSlots: make(chan struct{}, cfg.MaxSessions), closed: make(chan struct{}),
	}
	if !cfg.SkipRecovery {
		if cfg.RecoveryGuard != nil {
			if err := cfg.RecoveryGuard(); err != nil {
				return nil, fmt.Errorf("refuse recovery: %w", err)
			}
		}
		if err := cfg.Store.MarkInterrupted(context.Background()); err != nil {
			return nil, fmt.Errorf("mark interrupted runs: %w", err)
		}
	}
	return runner, nil
}

// Start validates the RunSpec and starts one asynchronous Run.
func (r *Runner) Start(ctx context.Context, spec RunSpec) (*Run, error) {
	if err := ValidateRunSpec(spec); err != nil {
		return nil, err
	}
	spec.Policy = NormalizeRunPolicy(spec.Policy)
	// The planner and every executor hold a live session for the whole Run, so
	// MaxSessions is enforced here rather than by prompting the model.
	if required := 2; required > r.maxSessions {
		return nil, fmt.Errorf("run requires %d sessions but runner allows %d", required, r.maxSessions)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	releaseSessions, err := r.acquireSessions(ctx, 2)
	if err != nil {
		return nil, err
	}
	started := false
	defer func() {
		if !started {
			releaseSessions()
		}
	}()
	runID, err := r.ids.NewRunID()
	if err != nil {
		return nil, err
	}
	now := r.clock.Now().UTC()
	baseCtx := ctx
	var timeoutCancel context.CancelFunc
	if spec.Policy.Budget.WallTime > 0 {
		baseCtx, timeoutCancel = context.WithTimeout(baseCtx, spec.Policy.Budget.WallTime)
	}
	runCtx, cancel := context.WithCancel(baseCtx)
	if timeoutCancel != nil {
		inner := cancel
		cancel = func() { inner(); timeoutCancel() }
	}
	redactor := redactorForSpec(spec, r.secrets)
	r.redacting.register(runID, redactor)
	workspaceRoot := spec.WorkspaceRoot
	if workspaceRoot == "" {
		workspaceRoot = r.workspaceRoot
	}
	controller := newRunController(r, runID, runCtx, cancel, filepath.Join(workspaceRoot, runID), spec.Policy.Budget, redactor, spec.MaterializeSkills, r.clock)
	controller.maxObserveFailures = spec.Policy.Exploration.MaxAuthorityObserveFailures
	run := newRun(controller)
	descriptor := spec.Descriptor()
	specJSON, _ := json.Marshal(descriptor)
	specJSON = redactor.RedactJSON(specJSON)
	if err := r.store.CreateRun(ctx, RunRecord{ID: runID, Phase: RunPhaseQueued, Spec: specJSON, CreatedAt: now}); err != nil {
		r.redacting.unregister(runID)
		return nil, fmt.Errorf("create run: %w", err)
	}
	r.mu.Lock()
	r.runs[runID] = run
	r.mu.Unlock()
	createdEvent, eventErr := controller.canonicalEvent("harness", EventRunCreated, RunCreatedPayload{TaskID: spec.Task.ID, TargetID: spec.Target.ID})
	if eventErr != nil {
		cancel()
		r.mu.Lock()
		delete(r.runs, runID)
		r.mu.Unlock()
		r.redacting.unregister(runID)
		return nil, eventErr
	}
	createdEvent.Time = now
	if err := controller.emitEvent(createdEvent); err != nil {
		cancel()
		r.mu.Lock()
		delete(r.runs, runID)
		r.mu.Unlock()
		r.redacting.unregister(runID)
		return nil, err
	}
	started = true
	go func() {
		defer releaseSessions()
		defer r.redacting.unregister(runID)
		controller.run(spec)
	}()
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

// Resume continues an interrupted Run from its newest committed board
// revision. The replayed board is re-validated before any session starts; a
// damaged or inconsistent snapshot fails closed. Sessions, workspace and
// provisioned targets are re-created, never assumed.
func (r *Runner) Resume(ctx context.Context, runID string, spec RunSpec) (*Run, error) {
	if err := ValidateRunSpec(spec); err != nil {
		return nil, err
	}
	spec.Policy = NormalizeRunPolicy(spec.Policy)
	if ctx == nil {
		ctx = context.Background()
	}
	record, err := r.store.GetRun(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("load run %s: %w", runID, err)
	}
	if record.Phase == RunPhaseTerminal {
		return nil, fmt.Errorf("run %s is already terminal", runID)
	}
	commit, err := r.store.RestoreBoardState(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("restore board for run %s: %w", runID, err)
	}
	releaseSessions, err := r.acquireSessions(ctx, 2)
	if err != nil {
		return nil, err
	}
	started := false
	defer func() {
		if !started {
			releaseSessions()
		}
	}()
	now := r.clock.Now().UTC()
	baseCtx := ctx
	var timeoutCancel context.CancelFunc
	if spec.Policy.Budget.WallTime > 0 {
		baseCtx, timeoutCancel = context.WithTimeout(baseCtx, spec.Policy.Budget.WallTime)
	}
	runCtx, cancel := context.WithCancel(baseCtx)
	if timeoutCancel != nil {
		inner := cancel
		cancel = func() { inner(); timeoutCancel() }
	}
	redactor := redactorForSpec(spec, r.secrets)
	r.redacting.register(runID, redactor)
	workspaceRoot := spec.WorkspaceRoot
	if workspaceRoot == "" {
		workspaceRoot = r.workspaceRoot
	}
	controller := newRunController(r, runID, runCtx, cancel, filepath.Join(workspaceRoot, runID), spec.Policy.Budget, redactor, spec.MaterializeSkills, r.clock)
	controller.maxObserveFailures = spec.Policy.Exploration.MaxAuthorityObserveFailures
	controller.resumeCommit = &commit
	controller.boardRevision = commit.Revision
	controller.apply(runTransition{kind: runTransitionStarted, at: now})
	run := newRun(controller)
	r.mu.Lock()
	r.runs[runID] = run
	r.mu.Unlock()
	resumedEvent, eventErr := controller.canonicalEvent("harness", EventRunResumed, RunResumedPayload{BoardRevision: commit.Revision})
	if eventErr != nil {
		cancel()
		r.mu.Lock()
		delete(r.runs, runID)
		r.mu.Unlock()
		r.redacting.unregister(runID)
		return nil, eventErr
	}
	resumedEvent.Time = now
	if err := controller.emitEvent(resumedEvent); err != nil {
		cancel()
		r.mu.Lock()
		delete(r.runs, runID)
		r.mu.Unlock()
		r.redacting.unregister(runID)
		return nil, err
	}
	started = true
	go func() {
		defer releaseSessions()
		defer r.redacting.unregister(runID)
		controller.run(spec)
	}()
	return run, nil
}

// Close cancels live Runs and waits for them to finish persisting before the
// store is closed. Closing a canonical store under a live Run would break the
// audit, so a run that refuses to stop within CloseTimeout is reported instead.
func (r *Runner) Close(ctx context.Context) error {
	r.closeOnce.Do(func() { close(r.closed) })
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	runs := make([]*Run, 0, len(r.runs))
	for _, run := range r.runs {
		runs = append(runs, run)
	}
	r.mu.Unlock()
	for _, run := range runs {
		_ = run.Cancel(ctx)
	}
	waitCtx := ctx
	if r.closeTimeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, r.closeTimeout)
		defer cancel()
	}
	for _, run := range runs {
		select {
		case <-run.Done():
		case <-waitCtx.Done():
			return fmt.Errorf("close runner: run %s did not finish: %w", run.ID, waitCtx.Err())
		}
	}
	return r.store.Close()
}

func (r *Runner) acquireSessions(ctx context.Context, count int) (func(), error) {
	release := func(acquired int) func() {
		return func() {
			for i := 0; i < acquired; i++ {
				<-r.sessionSlots
			}
		}
	}
	select {
	case <-r.closed:
		return nil, errors.New("runner is closed")
	default:
	}
	acquired := 0
	for acquired < count {
		select {
		case r.sessionSlots <- struct{}{}:
			acquired++
		case <-ctx.Done():
			release(acquired)()
			return nil, ctx.Err()
		case <-r.closed:
			release(acquired)()
			return nil, errors.New("runner is closed")
		}
	}
	return release(acquired), nil
}
