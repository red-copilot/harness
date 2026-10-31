package harness_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

type completionRaceBackend struct {
	mu         sync.Mutex
	done       bool
	calls      int
	closeCalls int
	closeErr   error
}

func (b *completionRaceBackend) Provision(_ context.Context, req harness.ProvisionRequest) (harness.TargetBinding, error) {
	return harness.TargetBinding{TargetID: req.Target.ID, Isolation: req.Target.Isolation}, nil
}

func (b *completionRaceBackend) Observe(context.Context, harness.TargetBinding) (harness.Completion, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	if b.done {
		return harness.Completion{Done: true, Success: true, Score: 42, Message: "backend authority"}, nil
	}
	return harness.Completion{}, nil
}

func (b *completionRaceBackend) Close(context.Context, harness.TargetBinding) error {
	b.mu.Lock()
	b.closeCalls++
	err := b.closeErr
	b.mu.Unlock()
	return err
}

func (b *completionRaceBackend) closeCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closeCalls
}

type provisionEventFailureStore struct {
	harness.Store
	err error
}

func (s *provisionEventFailureStore) AppendEvent(ctx context.Context, event harness.Event) (harness.Event, error) {
	if event.Type == harness.EventRunProvisioned {
		return harness.Event{}, s.err
	}
	return s.Store.AppendEvent(ctx, event)
}

func (b *completionRaceBackend) complete() {
	b.mu.Lock()
	b.done = true
	b.mu.Unlock()
}

func (b *completionRaceBackend) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

type completionDuringVerifier struct{ backend *completionRaceBackend }

func (v completionDuringVerifier) Verify(context.Context, harness.VerifyRequest) (harness.Verdict, error) {
	v.backend.complete()
	time.Sleep(30 * time.Millisecond)
	return harness.Verdict{Success: false, Message: "verifier lost the authority race"}, nil
}

func TestBackendCompletionDuringVerifierWins(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	backend := &completionRaceBackend{}
	attachBackend(&spec, backend)
	spec.Verifier = completionDuringVerifier{backend: backend}
	runner := newTestRunnerWithPoll(t, store, 5*time.Millisecond)
	result, err := runner.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != harness.RunOutcomeSucceeded || result.Verdict == nil || result.Verdict.Message != "backend authority" || result.Verdict.Score != 42 {
		t.Fatalf("result = %+v, want backend authority", result)
	}
}

func TestBackendWatcherStopsBeforeRunReturns(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	backend := &completionRaceBackend{}
	attachBackend(&spec, backend)
	spec.Verifier = fakeVerifier{verdict: harness.Verdict{Success: true}}
	runner := newTestRunnerWithPoll(t, store, 5*time.Millisecond)
	if _, err := runner.Run(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	before := backend.callCount()
	time.Sleep(40 * time.Millisecond)
	after := backend.callCount()
	if after != before {
		t.Fatalf("backend was observed after Run returned: before=%d after=%d", before, after)
	}
}

func TestBackendOnlyRunWaitsForDelayedCompletion(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	backend := &completionRaceBackend{}
	attachBackend(&spec, backend)
	spec.Verifier = nil
	spec.ReasonStrategy = nil
	spec.Reasoner.Runtime = &fakeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		return promptReportSession(req.WorkspaceDir, `{"board_revision":1,"goal_candidate":{"from":["origin"],"description":"challenge appears complete"}}`)
	}}
	spec.Executor.Runtime = &fakeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		return promptReportSession(req.WorkspaceDir, `{"phase":"failed","error":"submitted"}`)
	}}
	runner := newTestRunnerWithPoll(t, store, 5*time.Millisecond)
	go func() {
		time.Sleep(30 * time.Millisecond)
		backend.complete()
	}()
	result, err := runner.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != harness.RunOutcomeSucceeded || result.Verdict == nil || result.Verdict.Authority != harness.VerdictAuthorityBackend {
		t.Fatalf("result = %+v, want delayed backend success", result)
	}
	events, err := store.ListEvents(context.Background(), result.RunID, 0)
	if err != nil {
		t.Fatal(err)
	}
	recorded := false
	for _, event := range events {
		if event.Type == harness.EventReasonCompleted && strings.Contains(string(event.Data), `"goal_candidate"`) {
			recorded = true
		}
	}
	if !recorded {
		t.Fatal("reason.completed event did not record the goal candidate")
	}
}

func TestProvisionedTargetClosedWhenProvisionEventFails(t *testing.T) {
	inner := newMemoryStore()
	appendErr := errors.New("provision event failed")
	store := &provisionEventFailureStore{Store: inner, err: appendErr}
	spec := integrationSpec(t, inner)
	backend := &completionRaceBackend{}
	attachBackend(&spec, backend)
	spec.Verifier = fakeVerifier{verdict: harness.Verdict{Success: true}}

	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err == nil || !errors.Is(err, appendErr) {
		t.Fatalf("err = %v, want provision event failure", err)
	}
	if result.Outcome != harness.RunOutcomeFailed {
		t.Fatalf("outcome = %q, want failed", result.Outcome)
	}
	if got := backend.closeCount(); got != 1 {
		t.Fatalf("target close calls = %d, want 1", got)
	}
}

func TestSessionCleanupFailureDowngradesSuccess(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.Verifier = fakeVerifier{verdict: harness.Verdict{Success: true}}
	closeErr := errors.New("session close failed")
	factory := &fakeRuntime{new: func(harness.RuntimeRequest) harness.AgentSession {
		return &fakeSession{events: make(chan harness.AgentEvent, 4), closeErr: closeErr}
	}}
	spec.Reasoner.Runtime = factory
	spec.Executor.Runtime = factory
	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "session close failed") {
		t.Fatalf("err = %v, want session cleanup failure", err)
	}
	if result.Outcome != harness.RunOutcomeFailed {
		t.Fatalf("outcome = %q, want failed", result.Outcome)
	}
	if len(result.Sessions) == 0 {
		t.Fatal("missing session snapshots")
	}
	failedSessions := 0
	for _, session := range result.Sessions {
		if session.Status == harness.SessionFailed {
			failedSessions++
		}
	}
	if failedSessions == 0 {
		t.Fatalf("sessions = %+v, want at least one failed cleanup", result.Sessions)
	}
}

func newTestRunnerWithPoll(t *testing.T, store harness.Store, poll time.Duration) *harness.Runner {
	t.Helper()
	runner, err := harness.NewRunner(harness.Config{Store: store, Starter: noopStarter{}, WorkspaceRoot: t.TempDir(), PollInterval: poll, SkipRecovery: true})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

type observeScriptBackend struct {
	mu         sync.Mutex
	remaining  int
	errorsSeen int
}

func (b *observeScriptBackend) Provision(_ context.Context, req harness.ProvisionRequest) (harness.TargetBinding, error) {
	return harness.TargetBinding{TargetID: req.Target.ID, Isolation: req.Target.Isolation}, nil
}

func (b *observeScriptBackend) Observe(context.Context, harness.TargetBinding) (harness.Completion, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.remaining > 0 {
		b.remaining--
		b.errorsSeen++
		return harness.Completion{}, errors.New("transient backend error")
	}
	return harness.Completion{Done: true, Success: true, Message: "backend authority"}, nil
}

func (b *observeScriptBackend) Close(context.Context, harness.TargetBinding) error { return nil }

func (b *observeScriptBackend) errorCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.errorsSeen
}

type slowReasonStrategy struct{ delay time.Duration }

func (s slowReasonStrategy) Prompt(harness.ReasonRequest) (string, error) {
	time.Sleep(s.delay)
	return "reason", nil
}
func (s slowReasonStrategy) Parse(req harness.ReasonRequest, _ harness.TurnResult) (harness.ReasonReport, error) {
	return harness.ReasonReport{BoardRevision: req.BoardRevision}, nil
}

func TestBackendFinalObserveErrorFailsClosed(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	backend := &observeScriptBackend{remaining: 1 << 30}
	attachBackend(&spec, backend)
	spec.ReasonStrategy = noIntentReasonStrategy{}
	spec.Verifier = fakeVerifier{verdict: harness.Verdict{Success: true}}

	result, err := newTestRunnerWithPoll(t, store, time.Hour).Run(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "observe backend authority") {
		t.Fatalf("err = %v, want authority observe failure", err)
	}
	if result.Outcome != harness.RunOutcomeFailed {
		t.Fatalf("outcome = %q, want failed", result.Outcome)
	}
}

func TestBackendWatcherFailsAfterConsecutiveErrors(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	backend := &observeScriptBackend{remaining: 1 << 30}
	attachBackend(&spec, backend)
	spec.ReasonStrategy = slowReasonStrategy{delay: 120 * time.Millisecond}
	spec.Policy.Exploration.MaxReasonerWallTime = time.Second
	spec.Verifier = fakeVerifier{verdict: harness.Verdict{Success: true}}

	result, err := newTestRunnerWithPoll(t, store, 5*time.Millisecond).Run(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "backend authority unavailable") {
		t.Fatalf("err = %v, want watcher fail-closed", err)
	}
	if result.Outcome != harness.RunOutcomeFailed {
		t.Fatalf("outcome = %q, want failed", result.Outcome)
	}
	if backend.errorCount() < 3 {
		t.Fatalf("observe errors = %d, want >= 3", backend.errorCount())
	}
}

func TestBackendWatcherRecoversFromTransientErrors(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	backend := &observeScriptBackend{remaining: 2}
	attachBackend(&spec, backend)
	spec.ReasonStrategy = slowReasonStrategy{delay: 120 * time.Millisecond}
	spec.Policy.Exploration.MaxReasonerWallTime = time.Second

	result, err := newTestRunnerWithPoll(t, store, 5*time.Millisecond).Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != harness.RunOutcomeSucceeded || result.Verdict == nil || result.Verdict.Authority != harness.VerdictAuthorityBackend {
		t.Fatalf("result = %+v, want backend success after transient errors", result)
	}
}

func TestTargetCleanupFailureDowngradesSuccess(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	backend := &completionRaceBackend{closeErr: errors.New("target close failed")}
	spec.Provisioner = backend
	spec.Backend = nil
	spec.Verifier = fakeVerifier{verdict: harness.Verdict{Success: true}}
	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "target close failed") {
		t.Fatalf("err = %v, want target cleanup failure", err)
	}
	if result.Outcome != harness.RunOutcomeFailed || result.Verdict == nil || !result.Verdict.Success {
		t.Fatalf("result = %+v, want failed Run with preserved verifier verdict", result)
	}
}
