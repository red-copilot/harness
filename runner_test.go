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
	spec.Backend = &fakeBackend{completion: harness.Completion{Done: true, Success: true, Score: 9}}
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
	runner, err := harness.NewRunner(harness.Config{Store: store, WorkspaceRoot: t.TempDir(), SkipRecovery: true})
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
	spec.Backend = backend
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
	if err := harness.ValidateRunSpec(spec); err == nil || !strings.Contains(err.Error(), "authority") {
		t.Fatalf("err = %v, want authority validation", err)
	}
}

func TestRunnerMarksUnfinishedRunsInterrupted(t *testing.T) {
	store := newMemoryStore()
	_, err := harness.NewRunner(harness.Config{Store: store, WorkspaceRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if store.markInterruptedCalls != 1 {
		t.Fatalf("MarkInterrupted calls = %d, want 1", store.markInterruptedCalls)
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
	case s.events <- harness.AgentEvent{Type: "agent_settled", Data: json.RawMessage(`{"type":"agent_settled"}`)}:
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
