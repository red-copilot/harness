package kernel

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/red-copilot/harness/core"
	"github.com/red-copilot/harness/engine"
)

// Runner owns v5 Run lifecycle and adapter composition.
type Runner struct {
	store         engine.V5Store
	clock         engine.Clock
	ids           engine.IDSource
	secrets       engine.SecretSource
	workspace     engine.Workspace
	workspaceRoot string
	maxSessions   int
	pollInterval  time.Duration
	closeTimeout  time.Duration
	finalizeWait  time.Duration
	slots         chan struct{}
	closed        chan struct{}
	closeOnce     sync.Once

	mu   sync.Mutex
	runs map[string]*Run
}

// New validates the composition and returns a Runner. It never inspects or
// mutates historical Runs: recovery is explicit and operator-driven.
func New(cfg Config) (*Runner, error) {
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
	if cfg.Workspace == nil {
		return nil, errors.New("runner workspace is required")
	}
	if cfg.MaxSessions < 0 || cfg.PollInterval < 0 || cfg.CloseTimeout < 0 || cfg.FinalizeTimeout < 0 {
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
	if cfg.FinalizeTimeout == 0 {
		cfg.FinalizeTimeout = 15 * time.Second
	}
	if cfg.WorkspaceRoot == "" {
		cfg.WorkspaceRoot = ".harness/workspaces"
	}
	return &Runner{
		store: cfg.Store, clock: cfg.Clock, ids: cfg.IDs, secrets: cfg.Secrets,
		workspace: cfg.Workspace, workspaceRoot: cfg.WorkspaceRoot,
		maxSessions: cfg.MaxSessions, pollInterval: cfg.PollInterval,
		closeTimeout: cfg.CloseTimeout, finalizeWait: cfg.FinalizeTimeout,
		slots: make(chan struct{}, cfg.MaxSessions), closed: make(chan struct{}),
		runs: map[string]*Run{},
	}, nil
}

// Start creates a Run and drives it asynchronously.
func (r *Runner) Start(ctx context.Context, spec Spec) (*Run, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	spec.Policy = core.NormalizeRunPolicy(spec.Policy)
	if required := 2; required > r.maxSessions {
		return nil, fmt.Errorf("run requires %d sessions but runner allows %d", required, r.maxSessions)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	release, err := r.acquire(ctx, 2)
	if err != nil {
		return nil, err
	}
	started := false
	defer func() {
		if !started {
			release()
		}
	}()
	runID, err := r.ids.NewRunID()
	if err != nil {
		return nil, err
	}
	now := r.clock.Now().UTC()
	redactor := r.redactorFor(spec)
	sealedManifest, err := redactor.SealManifest(spec.Manifest(runID, now))
	if err != nil {
		return nil, fmt.Errorf("seal manifest: %w", err)
	}
	manifest := sealedManifest.Manifest()
	digest, err := manifest.Digest()
	if err != nil {
		return nil, err
	}
	record, err := core.NewRecord(runID, 1, core.RecordRunCreated, now, core.RunCreatedRecord{
		TaskID: manifest.Task.ID, TargetID: manifest.Target.ID, ManifestDigest: digest,
		ReasonerID: manifest.Reasoner.ID, ExecutorID: manifest.Executor.ID,
	}, digest)
	if err != nil {
		return nil, err
	}
	first, err := redactor.SealBatch(core.Batch{
		RunID: runID, ExpectedHead: 0, State: &core.State{RunID: runID, Phase: core.PhaseQueued},
		BoardRevision: 0, Records: []core.Record{record},
	})
	if err != nil {
		return nil, err
	}
	state, err := r.store.Create(ctx, sealedManifest, first)
	if err != nil {
		return nil, fmt.Errorf("create run: %w", err)
	}
	runCtx, cancel := r.runContext(ctx, manifest.Policy.Budget.WallTime)
	active := r.newActiveRun(runCtx, cancel, spec, manifest, digest, state, *core.NewBoard(runID), false)
	run := &Run{ID: runID, active: active}
	r.mu.Lock()
	r.runs[runID] = run
	r.mu.Unlock()
	started = true
	go func() {
		defer release()
		active.drive()
		r.mu.Lock()
		delete(r.runs, runID)
		r.mu.Unlock()
	}()
	return run, nil
}

// Run starts a Run and waits for its terminal result.
func (r *Runner) Run(ctx context.Context, spec Spec) (core.RunResult, error) {
	run, err := r.Start(ctx, spec)
	if err != nil {
		return core.RunResult{}, err
	}
	return run.Wait(ctx)
}

// Resume continues a non-terminal Run from its persisted manifest and journal.
// The caller supplies adapter bindings only; task, target, policy and agent
// identities come from the manifest and must match.
func (r *Runner) Resume(ctx context.Context, runID string, spec Spec) (*Run, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	live := r.runs[runID] != nil
	r.mu.Unlock()
	if live {
		return nil, fmt.Errorf("run %s is already active", runID)
	}
	restored, err := r.store.Replay(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("replay run %s: %w", runID, err)
	}
	if restored.State.Phase == core.PhaseTerminal {
		return nil, fmt.Errorf("run %s is already terminal", runID)
	}
	if err := bindingsMatch(restored.Manifest, spec); err != nil {
		return nil, err
	}
	release, err := r.acquire(ctx, 2)
	if err != nil {
		return nil, err
	}
	started := false
	defer func() {
		if !started {
			release()
		}
	}()
	manifest := restored.Manifest
	digest, err := manifest.Digest()
	if err != nil {
		return nil, err
	}
	runCtx, cancel := r.runContext(ctx, manifest.Policy.Budget.WallTime)
	active := r.newActiveRun(runCtx, cancel, spec, manifest, digest, restored.State, restored.Board, true)
	run := &Run{ID: runID, active: active}
	r.mu.Lock()
	if existing := r.runs[runID]; existing != nil {
		r.mu.Unlock()
		cancel()
		return nil, fmt.Errorf("run %s is already active", runID)
	}
	r.runs[runID] = run
	r.mu.Unlock()
	started = true
	go func() {
		defer release()
		active.drive()
		r.mu.Lock()
		delete(r.runs, runID)
		r.mu.Unlock()
	}()
	return run, nil
}

// Close cancels live Runs, waits for them to finalize, then closes the store.
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

func (r *Runner) runContext(ctx context.Context, wall time.Duration) (context.Context, context.CancelFunc) {
	if wall > 0 {
		base, timeoutCancel := context.WithTimeout(ctx, wall)
		child, cancel := context.WithCancel(base)
		return child, func() { cancel(); timeoutCancel() }
	}
	return context.WithCancel(ctx)
}

func (r *Runner) redactorFor(spec Spec) *core.Redactor {
	values := append([]string(nil), spec.KnownSecrets...)
	lookup := func(string) (string, bool) { return "", false }
	if r.secrets != nil {
		lookup = r.secrets.LookupEnv
	}
	agents := []engine.AgentSpec{spec.Reasoner, spec.Executor}
	for _, key := range spec.SecretEnv {
		if value, ok := lookup(key); ok {
			values = append(values, value)
		}
		for _, agent := range agents {
			if value, ok := agent.Env[key]; ok {
				values = append(values, value)
			}
		}
	}
	return core.NewRedactor(values...)
}

func (r *Runner) newActiveRun(ctx context.Context, cancel context.CancelFunc, spec Spec, manifest core.Manifest, digest string, state core.State, board core.Board, resumed bool) *activeRun {
	root := spec.WorkspaceRoot
	if root == "" {
		root = r.workspaceRoot
	}
	events := make(chan core.Record, 256)
	active := &activeRun{
		runner: r, id: manifest.RunID, spec: spec, manifest: manifest, digest: digest,
		ctx: ctx, cancel: cancel, events: events, done: make(chan struct{}),
		root: root, resumed: resumed, sessions: map[string]engine.AgentSession{},
		resultCh: make(chan attemptResult, 2), reasonCh: make(chan reasonResult, 1),
	}
	active.writer = newWriter(r.store, r.redactorFor(spec), r.clock, manifest, digest, state, board, func(record core.Record) {
		select {
		case events <- record:
		default:
		}
	})
	if source, ok := r.ids.(entityIDSource); ok {
		active.writer.newEntity = source.NewEntityID
	}
	return active
}

type entityIDSource interface {
	NewEntityID(runID, kind string) (string, error)
}

func bindingsMatch(manifest core.Manifest, spec Spec) error {
	if manifest.Reasoner.ID != spec.Reasoner.ID {
		return fmt.Errorf("reasoner %q does not match manifest %q", spec.Reasoner.ID, manifest.Reasoner.ID)
	}
	if manifest.Executor.ID != spec.Executor.ID {
		return fmt.Errorf("executor %q does not match manifest %q", spec.Executor.ID, manifest.Executor.ID)
	}
	verifierID := spec.VerifierID
	if verifierID == "" {
		verifierID = "verifier"
	}
	if manifest.Verifier.ID != verifierID {
		return fmt.Errorf("verifier %q does not match manifest %q", verifierID, manifest.Verifier.ID)
	}
	return nil
}

func (r *Runner) acquire(ctx context.Context, count int) (func(), error) {
	select {
	case <-r.closed:
		return nil, errors.New("runner is closed")
	default:
	}
	release := func(acquired int) func() {
		return func() {
			for i := 0; i < acquired; i++ {
				<-r.slots
			}
		}
	}
	acquired := 0
	for acquired < count {
		select {
		case r.slots <- struct{}{}:
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
