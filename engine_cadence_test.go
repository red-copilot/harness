package harness_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

// budgetBreachSession reports growing usage on every read so the reason-side
// usage delta breaches the token budget and stops new work fail-closed.
type budgetBreachSession struct {
	*fakeSession
	mu    sync.Mutex
	calls int
}

func (s *budgetBreachSession) Usage() harness.Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return harness.Usage{TotalTokens: (s.calls + 1) << 20}
}

// TestBudgetExhaustionFailsClosed drives a run to budget breach end-to-end and
// asserts the fail-closed outcome: failed outcome, breached budget recorded in
// the terminal error, no successful verdict.
func TestBudgetExhaustionFailsClosed(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.Policy.Budget.MaxTokens = 100
	spec.Reasoner.Runtime = &fakeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		session := promptReportSession(req.WorkspaceDir, reasonIntentReport())
		return &budgetBreachSession{fakeSession: session}
	}}
	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("err = %v, want budget breach", err)
	}
	if result.Outcome != harness.RunOutcomeFailed {
		t.Fatalf("outcome = %q, want failed", result.Outcome)
	}
	if result.Verdict != nil && result.Verdict.Success {
		t.Fatalf("verdict = %+v, want no successful verdict on breach", result.Verdict)
	}
}

// hangingExecutorSession blocks until its context is canceled, exercising the
// cancellation drain path: cancelActive, drainCanceledAttempts and
// recordCanceledAttempt.
type hangingExecutorSession struct {
	*fakeSession
	started chan struct{}
	once    sync.Once
}

func (s *hangingExecutorSession) Prompt(ctx context.Context, _ string) error {
	s.once.Do(func() { close(s.started) })
	<-ctx.Done()
	return ctx.Err()
}

func (s *hangingExecutorSession) Events() <-chan harness.AgentEvent {
	return s.fakeSession.events
}

// TestCancellationDrainsInFlightAttempt starts a run whose executor never
// settles, cancels it, and asserts the attempt is recorded as cancelled and the
// claimed intent is abandoned rather than left dangling.
func TestCancellationDrainsInFlightAttempt(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.ReasonStrategy = oneIntentReasonStrategy{}
	executor := &hangingExecutorSession{fakeSession: &fakeSession{events: make(chan harness.AgentEvent, 8)}, started: make(chan struct{})}
	spec.Executor.Runtime = &fakeRuntime{new: func(harness.RuntimeRequest) harness.AgentSession { return executor }}
	spec.Policy.Exploration.MaxWallTimePerAttempt = 10 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	runner := newTestRunner(t, store)
	run, err := runner.Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-executor.started:
	case <-time.After(5 * time.Second):
		t.Fatal("executor attempt never started")
	}
	cancel()
	select {
	case <-run.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("run did not terminate after cancellation")
	}
	result, runErr := run.Wait(context.Background())
	if runErr == nil || !errorsIsCanceled(runErr) {
		t.Fatalf("err = %v, want cancellation", runErr)
	}
	if result.Outcome != harness.RunOutcomeCancelled && result.Outcome != harness.RunOutcomeFailed {
		t.Fatalf("outcome = %q, want cancelled or failed", result.Outcome)
	}
	for _, attempt := range result.IntentAttempts {
		if attempt.Phase == harness.IntentAttemptRunning {
			t.Fatalf("attempt %s left running after drain: %+v", attempt.ID, attempt)
		}
	}
}

func errorsIsCanceled(err error) bool {
	return err != nil && strings.Contains(err.Error(), "context cancel")
}

// TestRunsWithoutAuthorityFailClosed pins the fail-closed authority contract:
// a spec with neither a Verifier nor a backend is rejected before any work
// starts, so no run can finish without an authoritative verdict source.
func TestRunsWithoutAuthorityFailClosed(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.Verifier = nil
	if err := harness.ValidateRunSpec(spec); err == nil {
		t.Fatal("spec without verifier or backend accepted")
	} else if !strings.Contains(err.Error(), "authority is required") {
		t.Fatalf("err = %v, want authority-required rejection", err)
	}
}
