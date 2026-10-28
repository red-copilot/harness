package engine

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

// controllerTestStore embeds the Store interface so a controller test only
// needs to implement the persistence calls exercised by finalization.
type controllerTestStore struct {
	Store

	mu              sync.Mutex
	runs            map[string]RunRecord
	events          []Event
	finalizeCalls   int
	finalizeStarted chan struct{}
	releaseFinalize <-chan struct{}
	appendStarted   chan struct{}
	releaseAppend   <-chan struct{}
	appendErr       error
	finalizeErr     error
	updateCalls     int
}

func (s *controllerTestStore) GetRun(_ context.Context, id string) (RunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs[id], nil
}

func (s *controllerTestStore) UpdateRun(_ context.Context, record RunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateCalls++
	s.runs[record.ID] = record
	return nil
}

func (s *controllerTestStore) AppendEvent(_ context.Context, event Event) (Event, error) {
	if s.appendStarted != nil {
		close(s.appendStarted)
	}
	if s.releaseAppend != nil {
		<-s.releaseAppend
	}
	if s.appendErr != nil {
		return Event{}, s.appendErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	event.Seq = int64(len(s.events) + 1)
	if event.SchemaVersion == 0 {
		event.SchemaVersion = 1
	}
	s.events = append(s.events, event)
	return event, nil
}

func (s *controllerTestStore) Finalize(_ context.Context, record FinalizationRecord) (Event, error) {
	if s.finalizeStarted != nil {
		close(s.finalizeStarted)
	}
	if s.releaseFinalize != nil {
		<-s.releaseFinalize
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finalizeCalls++
	if s.finalizeErr != nil {
		return Event{}, s.finalizeErr
	}
	s.runs[record.Run.ID] = record.Run
	event := record.Event
	event.Seq = int64(len(s.events) + 1)
	if event.SchemaVersion == 0 {
		event.SchemaVersion = 1
	}
	s.events = append(s.events, event)
	return event, nil
}

func TestFinalizeFailureDoesNotPersistTerminalRunSeparately(t *testing.T) {
	finalizeErr := errors.New("finalize transaction failed")
	inner := &controllerTestStore{
		runs:        map[string]RunRecord{"run-1": {ID: "run-1", Phase: RunPhaseRunning, Status: StatusRunning}},
		finalizeErr: finalizeErr,
	}
	store := newRedactingStore(inner)
	store.register("run-1", NewRedactor())
	runner := &Runner{store: store, redacting: store}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	controller := newRunController(runner, "run-1", ctx, cancel, t.TempDir(), Budget{}, NewRedactor())

	controller.finishExecution(RunOutcomeFailed, errors.New("original failure"), &Verdict{Success: false})

	if inner.updateCalls != 0 {
		t.Fatalf("UpdateRun calls = %d, want 0 after Finalize failure", inner.updateCalls)
	}
	if inner.finalizeCalls != 1 {
		t.Fatalf("Finalize calls = %d, want 1", inner.finalizeCalls)
	}
	if record := inner.runs["run-1"]; record.Phase == RunPhaseTerminal {
		t.Fatalf("Run was persisted terminal without Finalize: %+v", record)
	}
	if len(inner.events) != 0 {
		t.Fatalf("events = %+v, want no uncommitted finalization event", inner.events)
	}
	result, err := controller.waitResult()
	if result.Outcome != RunOutcomeFailed || !errors.Is(err, finalizeErr) {
		t.Fatalf("result=%+v err=%v, want failed finalize error", result, err)
	}
}

func newFinalizationTestRun(t *testing.T) (*Run, *controllerTestStore) {
	t.Helper()
	inner := &controllerTestStore{runs: map[string]RunRecord{
		"run-1": {ID: "run-1", Phase: RunPhaseRunning, Status: StatusRunning},
	}}
	store := newRedactingStore(inner)
	store.register("run-1", NewRedactor())
	runner := &Runner{store: store, redacting: store}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	controller := newRunController(runner, "run-1", ctx, cancel, t.TempDir(), Budget{}, NewRedactor())
	run := newRun(controller)
	return run, inner
}

func TestControllerIsSingleWriterUnderConcurrentCompletion(t *testing.T) {
	run, store := newFinalizationTestRun(t)

	const contenders = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	panics := make(chan any, contenders)
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					panics <- recovered
				}
			}()
			<-start
			outcome := RunOutcomeSucceeded
			success := true
			if i%2 != 0 {
				outcome = RunOutcomeFailed
				success = false
			}
			run.controller.finishExecution(outcome, nil, &Verdict{Success: success})
		}(i)
	}
	close(start)
	wg.Wait()
	close(panics)

	for recovered := range panics {
		t.Fatalf("concurrent completion panicked: %v", recovered)
	}
	if store.finalizeCalls != 1 {
		t.Fatalf("finalize calls = %d, want exactly 1", store.finalizeCalls)
	}
	if len(store.events) != 1 || store.events[0].Type != EventRunFinalized {
		t.Fatalf("terminal events = %+v, want one run.finalized", store.events)
	}
	result, err := run.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait err = %v", err)
	}
	if err := ValidateRunState(result.Phase, result.Outcome); err != nil {
		t.Fatalf("invalid final run state: %v", err)
	}
}

func TestControllerSerializesControlTransitionsThroughApply(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	controller := newRunController(nil, "run-1", ctx, cancel, t.TempDir(), Budget{MaxTokens: 1000}, NewRedactor())

	if result := controller.apply(runTransition{kind: runTransitionStarted, at: time.Now().UTC()}); !result.applied {
		t.Fatal("start transition was rejected")
	}

	const updates = 128
	var wg sync.WaitGroup
	for i := 0; i < updates; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			controller.apply(runTransition{kind: runTransitionUsage, usage: Usage{TotalTokens: 1}})
		}()
	}
	wg.Wait()

	completion := Completion{Done: true, Success: true, Score: 9}
	controller.apply(runTransition{kind: runTransitionProgress, completion: &completion})
	controller.apply(runTransition{kind: runTransitionAuthorityCompletion, completion: &completion})
	controlErr := errors.New("canonical write failed")
	controller.apply(runTransition{kind: runTransitionControlError, err: controlErr})
	controller.apply(runTransition{kind: runTransitionStagnation})

	claim := controller.apply(runTransition{kind: runTransitionClaimFinalization, outcome: RunOutcomeFailed})
	if !claim.applied || claim.frozen == nil {
		t.Fatalf("terminal claim = %+v", claim)
	}
	if claim.frozen.usage.TotalTokens != updates {
		t.Fatalf("frozen usage = %d, want %d", claim.frozen.usage.TotalTokens, updates)
	}
	if claim.frozen.controlErr != controlErr {
		t.Fatalf("frozen control error = %v, want %v", claim.frozen.controlErr, controlErr)
	}
	if got := controller.backendCompletion(); got == nil || !got.Done || got.Score != 9 {
		t.Fatalf("authority snapshot = %+v", got)
	}
	if controller.progress().Score != 9 || !controller.stagnated() || controller.budgetExceeded() != nil {
		t.Fatalf("controller projections did not reflect applied transitions")
	}
	if result := controller.apply(runTransition{kind: runTransitionUsage, usage: Usage{TotalTokens: 1}}); result.applied {
		t.Fatal("post-claim usage transition was accepted")
	}

	const finalMessage = "single terminal write"
	if result := controller.apply(runTransition{
		kind: runTransitionFinalized, outcome: RunOutcomeFailed, err: controlErr,
		result: Result{RunID: "run-1", Phase: RunPhaseTerminal, Outcome: RunOutcomeFailed, Status: StatusFailed, Error: finalMessage},
	}); !result.applied {
		t.Fatalf("final transition rejected: %+v", result)
	}
	if result := controller.apply(runTransition{kind: runTransitionPhase, phase: RunPhaseRunning}); result.applied {
		t.Fatal("post-finalization phase transition was accepted")
	}
	final, err := controller.waitResult()
	if err != controlErr || final.Error != finalMessage || final.Status != StatusFailed {
		t.Fatalf("final snapshot = %+v, err=%v", final, err)
	}
	if controller.transitionSeq != updates+9 {
		t.Fatalf("transition revisions = %d, want %d; a control mutation bypassed apply", controller.transitionSeq, updates+9)
	}
}

func TestControllerRejectsEventWritesWhileFinalizationOwnsStream(t *testing.T) {
	release := make(chan struct{})
	inner := &controllerTestStore{
		runs:            map[string]RunRecord{"run-1": {ID: "run-1", Phase: RunPhaseRunning, Status: StatusRunning}},
		finalizeStarted: make(chan struct{}), releaseFinalize: release,
	}
	store := newRedactingStore(inner)
	store.register("run-1", NewRedactor())
	runner := &Runner{store: store, redacting: store}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	controller := newRunController(runner, "run-1", ctx, cancel, t.TempDir(), Budget{}, NewRedactor())
	run := newRun(controller)

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		run.controller.finishExecution(RunOutcomeFailed, errors.New("failed"), &Verdict{Success: false})
	}()
	<-inner.finalizeStarted

	emitResult := make(chan error, 1)
	go func() {
		emitResult <- controller.emitEvent(Event{RunID: "run-1", Source: "late", Type: EventAgent, Data: mustJSON(AgentEventPayload{NativeType: "test"})})
	}()
	select {
	case err := <-emitResult:
		t.Fatalf("event write escaped finalization barrier: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	close(release)
	<-finished
	if err := <-emitResult; !errors.Is(err, ErrRunClosed) {
		t.Fatalf("late event err = %v, want ErrRunClosed", err)
	}
	if len(inner.events) != 1 || inner.events[0].Type != EventRunFinalized {
		t.Fatalf("events = %+v, want only run.finalized", inner.events)
	}
}

func TestInFlightCanonicalEventFailureDowngradesSuccess(t *testing.T) {
	releaseAppend := make(chan struct{})
	appendErr := errors.New("canonical append failed")
	inner := &controllerTestStore{
		runs:          map[string]RunRecord{"run-1": {ID: "run-1", Phase: RunPhaseRunning, Status: StatusRunning}},
		appendStarted: make(chan struct{}), releaseAppend: releaseAppend, appendErr: appendErr,
	}
	store := newRedactingStore(inner)
	store.register("run-1", NewRedactor())
	runner := &Runner{store: store, redacting: store}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	controller := newRunController(runner, "run-1", ctx, cancel, t.TempDir(), Budget{}, NewRedactor())

	emitDone := make(chan error, 1)
	go func() {
		emitDone <- controller.emitEvent(Event{RunID: "run-1", Source: "harness", Type: EventAgent, Data: mustJSON(AgentEventPayload{NativeType: "test"})})
	}()
	<-inner.appendStarted

	finishDone := make(chan struct{})
	go func() {
		defer close(finishDone)
		controller.finishExecution(RunOutcomeSucceeded, nil, &Verdict{Success: true})
	}()
	time.Sleep(20 * time.Millisecond)
	close(releaseAppend)
	<-finishDone
	if err := <-emitDone; !errors.Is(err, appendErr) {
		t.Fatalf("append err = %v, want %v", err, appendErr)
	}
	result, err := controller.waitResult()
	if result.Outcome != RunOutcomeFailed || result.Status != StatusFailed {
		t.Fatalf("result = %+v, want failed due to canonical write", result)
	}
	if !errors.Is(err, appendErr) {
		t.Fatalf("final err = %v, want %v", err, appendErr)
	}
}

func TestControllerRejectsRunPhaseRewind(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	controller := newRunController(nil, "run-1", ctx, cancel, t.TempDir(), Budget{}, NewRedactor())
	if result := controller.apply(runTransition{kind: runTransitionPhase, phase: RunPhaseProvisioning}); !result.applied {
		t.Fatalf("queued -> provisioning rejected: %+v", result)
	}
	if result := controller.apply(runTransition{kind: runTransitionPhase, phase: RunPhaseRunning}); !result.applied {
		t.Fatalf("provisioning -> running rejected: %+v", result)
	}
	if result := controller.apply(runTransition{kind: runTransitionPhase, phase: RunPhaseQueued}); result.applied || result.validation == nil {
		t.Fatalf("running -> queued accepted: %+v", result)
	}
}

func TestUsageFromSessionsSumsTransportUsage(t *testing.T) {
	total := usageFromSessions([]SessionResult{
		{ID: "a", Usage: Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5, ModelCalls: 1, ToolCalls: 2, CostUSD: 0.1}},
		{ID: "b", Usage: Usage{InputTokens: 7, OutputTokens: 11, TotalTokens: 18, ModelCalls: 2, ToolCalls: 3, CostUSD: 0.2}},
	})
	if total.InputTokens != 9 || total.OutputTokens != 14 || total.TotalTokens != 23 || total.ModelCalls != 3 || total.ToolCalls != 5 || math.Abs(total.CostUSD-0.3) > 1e-9 {
		t.Fatalf("usage = %+v", total)
	}
}
