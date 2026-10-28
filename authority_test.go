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
	mu       sync.Mutex
	done     bool
	calls    int
	closeErr error
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
	return b.closeErr
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
		if session.Status == harness.StatusFailed {
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
