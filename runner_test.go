package harness_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

func TestRunnerUsesVerifierAsOnlySuccessSource(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.Verifier = fakeVerifier{verdict: harness.Verdict{Success: false, Message: "not verified"}}
	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != harness.StatusFailed || result.Verdict == nil || result.Verdict.Success {
		t.Fatalf("unexpected result: %#v", result)
	}

	store2 := newMemoryStore()
	spec2 := integrationSpec(t, store2)
	spec2.Verifier = fakeVerifier{verdict: harness.Verdict{Success: true, Message: "verified"}}
	result2, err := newTestRunner(t, store2).Run(context.Background(), spec2)
	if err != nil {
		t.Fatal(err)
	}
	if result2.Status != harness.StatusSucceeded || result2.Verdict == nil || !result2.Verdict.Success {
		t.Fatalf("unexpected result: %#v", result2)
	}
}

func TestBackendCompletionWinsAndCancelsWork(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.Verifier = nil
	attachBackend(&spec, &fakeBackend{completion: harness.Completion{Done: true, Success: true, Score: 9}})
	spec.Planner.Runtime = &fakeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		return &fakeSession{events: make(chan harness.AgentEvent), usage: harness.Usage{}}
	}}
	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != harness.StatusSucceeded || result.Verdict == nil || result.Verdict.Score != 9 {
		t.Fatalf("backend result = %#v", result)
	}
}

func TestRunnerPersistsFailureWhenAuditWriteFails(t *testing.T) {
	store := &failingEventStore{memoryStore: newMemoryStore(), failType: "agent_settled"}
	spec := integrationSpec(t, store.memoryStore)
	runner, err := harness.NewRunner(harness.Config{Store: store, Starter: noopStarter{}, WorkspaceRoot: t.TempDir(), SkipRecovery: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), spec)
	if err == nil {
		t.Fatal("expected audit write failure")
	}
	if result.Status != harness.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
}

func TestRunnerCancelClosesSessionsAndTarget(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	backend := &fakeBackend{completion: harness.Completion{}}
	attachBackend(&spec, backend)
	closed := make(chan struct{}, 1)
	spec.ExecutorStrategy = blockingExecutorStrategy{onPrompt: func(harness.AttemptRequest) {
		select {
		case <-closed:
		default:
			close(closed)
		}
	}}
	spec.PlannerStrategy = staticPlannerStrategy{proposal: harness.PlanProposal{CreateBranches: []harness.BranchProposal{{ParentID: "root", Topic: "web", Goal: "web", Hypothesis: "web", Target: "target-1", SuccessCondition: "yes", FailureCondition: "no"}}}}
	spec.ExecutorStrategy = noChildExecutorStrategy{}
	spec.Executors[0].Runtime = &fakeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		return &fakeSession{events: make(chan harness.AgentEvent), prompt: func(ctx context.Context, _ string) error { <-ctx.Done(); return ctx.Err() }}
	}}
	runner := newTestRunner(t, store)
	run, err := runner.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := run.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := run.Wait(context.Background())
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if result.Status != harness.StatusCancelled {
		t.Fatalf("status = %q, want cancelled", result.Status)
	}
	if backend.closeCalls == 0 {
		t.Fatal("backend was not closed")
	}
}

func TestRunnerMarksNoAuthoritativeVerdictAsFailed(t *testing.T) {
	spec := validRunSpec()
	spec.Verifier = nil
	spec.Backend = nil
	spec.Provisioner = nil
	if err := harness.ValidateRunSpec(spec); err == nil || !strings.Contains(err.Error(), "authority") {
		t.Fatalf("err = %v, want authority validation", err)
	}
}

func TestRunnerMarksUnfinishedRunsInterrupted(t *testing.T) {
	store := newMemoryStore()
	_, err := harness.NewRunner(harness.Config{Store: store, Starter: noopStarter{}, WorkspaceRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if store.markInterruptedCalls != 1 {
		t.Fatalf("MarkInterrupted calls = %d, want 1", store.markInterruptedCalls)
	}
}

func TestBackendCompletionConsultedAfterExplorationEnds(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.Verifier = nil
	attachBackend(&spec, &fakeBackend{completion: harness.Completion{Done: true, Success: true, Score: 7}})
	spec.PlannerStrategy = staticPlannerStrategy{proposal: harness.PlanProposal{CreateBranches: []harness.BranchProposal{{ParentID: "root", Topic: "web", Goal: "web", Hypothesis: "web", Target: "target-1", SuccessCondition: "yes", FailureCondition: "no"}}}}
	runner, err := harness.NewRunner(harness.Config{Store: store, Starter: noopStarter{}, WorkspaceRoot: t.TempDir(), PollInterval: time.Hour, SkipRecovery: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != harness.StatusSucceeded || result.Verdict == nil || result.Verdict.Score != 7 {
		t.Fatalf("result = %#v, want authoritative backend completion", result)
	}
}

type fakeSession struct {
	events chan harness.AgentEvent
	usage  harness.Usage
	mu     sync.Mutex
	aborts int
	closes int
	prompt func(context.Context, string) error
}

func (s *fakeSession) Prompt(ctx context.Context, prompt string) error {
	if s.prompt != nil {
		return s.prompt(ctx, prompt)
	}
	select {
	case s.events <- harness.AgentEvent{Type: "agent_settled", Kind: harness.AgentEventSettled, Data: json.RawMessage(`{"type":"agent_settled"}`)}:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}
func (s *fakeSession) Steer(context.Context, string) error { return nil }
func (s *fakeSession) Reset(context.Context) error         { return nil }
func (s *fakeSession) Events() <-chan harness.AgentEvent   { return s.events }
func (s *fakeSession) Logs() <-chan harness.LogEvent       { return nil }
func (s *fakeSession) Usage() harness.Usage                { return s.usage }
func (s *fakeSession) Abort(context.Context) error {
	s.mu.Lock()
	s.aborts++
	s.mu.Unlock()
	return nil
}
func (s *fakeSession) Close(context.Context) error {
	s.mu.Lock()
	s.closes++
	s.mu.Unlock()
	return nil
}

type fakeBackend struct {
	mu          sync.Mutex
	completion  harness.Completion
	closeCalls  int
	provisioned harness.Target
}

func (b *fakeBackend) Provision(context.Context, harness.ProvisionRequest) (harness.Target, error) {
	if b.provisioned.ID == "" {
		return harness.Target{ID: "target-1", Isolation: harness.IsolationCallerManaged}, nil
	}
	return b.provisioned, nil
}
func (b *fakeBackend) Observe(context.Context, harness.Target) (harness.Completion, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.completion.Done {
		completion := b.completion
		b.completion = harness.Completion{}
		return completion, nil
	}
	return harness.Completion{}, nil
}
func (b *fakeBackend) Close(context.Context, harness.Target) error {
	b.mu.Lock()
	b.closeCalls++
	b.mu.Unlock()
	return nil
}

type failingEventStore struct {
	*memoryStore
	failType string
}

func (s *failingEventStore) AppendEvent(ctx context.Context, event harness.Event) (harness.Event, error) {
	if event.Type == s.failType {
		return harness.Event{}, errors.New("event store unavailable")
	}
	return s.memoryStore.AppendEvent(ctx, event)
}

// The finalization event rides in the same transaction as the terminal record.
func (s *failingEventStore) Finalize(ctx context.Context, record harness.FinalizationRecord) (harness.Event, error) {
	if record.Event.Type == s.failType {
		return harness.Event{}, errors.New("event store unavailable")
	}
	return s.memoryStore.Finalize(ctx, record)
}

func TestConfigWiresProcessStarter(t *testing.T) {
	store := newMemoryStore()
	if _, err := harness.NewRunner(harness.Config{Store: store, WorkspaceRoot: t.TempDir(), SkipRecovery: true}); err == nil || !strings.Contains(err.Error(), "starter") {
		t.Fatalf("NewRunner without starter error = %v, want process starter error", err)
	}

	spec := integrationSpec(t, store)
	runtime := spec.Planner.Runtime.(*fakeRuntime)
	if _, err := newTestRunner(t, store).Run(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.requests) == 0 || runtime.requests[0].Starter == nil {
		t.Fatalf("planner session did not receive the runner process starter: %#v", runtime.requests)
	}
}

func TestRunWallTimeBudgetTerminates(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.Budget = harness.Budget{WallTime: 100 * time.Millisecond}
	spec.PlannerStrategy = staticPlannerStrategy{proposal: harness.PlanProposal{CreateBranches: []harness.BranchProposal{{ParentID: "root", Topic: "web", Goal: "web", Hypothesis: "web", Target: "target-1", SuccessCondition: "yes", FailureCondition: "no"}}}}
	spec.Executors[0].Runtime = &fakeRuntime{new: func(harness.RuntimeRequest) harness.AgentSession {
		return &fakeSession{events: make(chan harness.AgentEvent), prompt: func(ctx context.Context, _ string) error {
			<-ctx.Done()
			return ctx.Err()
		}}
	}}
	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if result.Status != harness.StatusTimedOut {
		t.Fatalf("status = %q, want timed_out", result.Status)
	}
}

func TestTokenBudgetStopsRun(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.Budget = harness.Budget{MaxTokens: 10}
	spec.Planner.Runtime = &fakeRuntime{new: func(harness.RuntimeRequest) harness.AgentSession {
		return &fakeSession{events: make(chan harness.AgentEvent, 4), usage: harness.Usage{TotalTokens: 1000}}
	}}
	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "token budget") {
		t.Fatalf("err = %v, want token budget error", err)
	}
	if result.Status != harness.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
}

func TestMaxSessionsCapsConcurrentAttempts(t *testing.T) {
	store := newMemoryStore()
	capped, err := harness.NewRunner(harness.Config{Store: store, Starter: noopStarter{}, MaxSessions: 2, WorkspaceRoot: t.TempDir(), SkipRecovery: true})
	if err != nil {
		t.Fatal(err)
	}
	spec := integrationSpec(t, store)
	spec.Executors = append(spec.Executors, harness.AgentSpec{ID: "exec-2", Runtime: &fakeRuntime{}})
	if _, err := capped.Start(context.Background(), spec); err == nil || !strings.Contains(err.Error(), "sessions") {
		t.Fatalf("Start err = %v, want session cap error", err)
	}

	allowed, err := harness.NewRunner(harness.Config{Store: store, Starter: noopStarter{}, MaxSessions: 2, WorkspaceRoot: t.TempDir(), SkipRecovery: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := allowed.Run(context.Background(), integrationSpec(t, store)); err != nil {
		t.Fatalf("run within session cap failed: %v", err)
	}
}

func TestCloseWaitsForLiveRuns(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	spec.Executors[0].Runtime = &fakeRuntime{new: func(harness.RuntimeRequest) harness.AgentSession {
		return &fakeSession{events: make(chan harness.AgentEvent), prompt: func(context.Context, string) error {
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
			return nil
		}}
	}}
	runner := newTestRunner(t, store)
	if _, err := runner.Start(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("executor session never started")
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := runner.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close err = %v, want deadline exceeded", err)
	}
}

type provisionOnlyBackend struct {
	provisioned int
	closed      int
}

func (b *provisionOnlyBackend) Provision(context.Context, harness.ProvisionRequest) (harness.Target, error) {
	b.provisioned++
	return harness.Target{ID: "target-1", Isolation: harness.IsolationCallerManaged}, nil
}
func (b *provisionOnlyBackend) Close(context.Context, harness.Target) error {
	b.closed++
	return nil
}

// TestProvisionerIsNotAnAuthority: owning a Target lifecycle must never be
// mistaken for proving the outcome, and a Run with only a provisioner is
// rejected without a verifier.
func TestProvisionerIsNotAnAuthority(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.Verifier = nil
	spec.Provisioner = &provisionOnlyBackend{}
	if err := harness.ValidateRunSpec(spec); err == nil || !strings.Contains(err.Error(), "authority") {
		t.Fatalf("ValidateRunSpec err = %v, want authority requirement", err)
	}

	store = newMemoryStore()
	spec = integrationSpec(t, store)
	backend := &provisionOnlyBackend{}
	spec.Provisioner = backend
	spec.Verifier = fakeVerifier{verdict: harness.Verdict{Success: true, Message: "verified"}}
	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != harness.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", result.Status)
	}
	if backend.provisioned != 1 || backend.closed != 1 {
		t.Fatalf("provisioned=%d closed=%d, want 1/1", backend.provisioned, backend.closed)
	}
}
