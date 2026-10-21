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

type recordingWorkflow struct {
	mu        sync.Mutex
	sawRunID  string
	sawTask   string
	sawTarget string
	sawWall   time.Duration
	openErrs  []error
}

func (w *recordingWorkflow) Execute(ctx context.Context, rt harness.WorkflowRuntime) (harness.WorkflowResult, error) {
	w.mu.Lock()
	w.sawRunID = rt.RunID()
	w.sawTask = rt.Task().ID
	w.sawTarget = rt.Target().ID
	w.sawWall = rt.Budget().WallTime
	w.mu.Unlock()

	lease, err := rt.OpenSession(ctx, rt.Agents()[0])
	if err != nil {
		return harness.WorkflowResult{}, err
	}
	if lease.ID != "agent-1" {
		return harness.WorkflowResult{}, errors.New("unexpected session id: " + lease.ID)
	}
	if err := rt.Emit(ctx, harness.Event{
		SessionID: lease.ID,
		Source:    "control",
		Type:      "workflow.observed",
	}); err != nil {
		return harness.WorkflowResult{}, err
	}
	return harness.WorkflowResult{}, nil
}

func TestRunnerExecutesInjectedWorkflow(t *testing.T) {
	store := newMemoryStore()
	factory := &fakeFactory{newSession: func(harness.SessionRequest) harness.AgentSession {
		return &fakeSession{result: harness.AgentResult{Status: harness.StatusSucceeded}}
	}}
	runner, err := harness.NewRunner(harness.Config{Store: store, DefaultFactory: factory, WorkspaceRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	workflow := &recordingWorkflow{}
	result, err := runner.Run(context.Background(), harness.RunSpec{
		Task:     harness.Task{ID: "workflow-task", Objective: "solve"},
		Target:   harness.Target{ID: "target-1"},
		Agents:   []harness.AgentSpec{{ID: "agent-1"}},
		Budget:   harness.Budget{WallTime: time.Minute},
		Workflow: workflow,
		Verifier: fakeVerifier{verdict: harness.Verdict{Success: true}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != harness.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", result.Status)
	}
	if len(result.Sessions) != 1 || result.Sessions[0].ID != "agent-1" {
		t.Fatalf("sessions = %#v", result.Sessions)
	}
	workflow.mu.Lock()
	defer workflow.mu.Unlock()
	if workflow.sawRunID == "" || workflow.sawRunID != result.RunID {
		t.Fatalf("workflow run id = %q, result = %q", workflow.sawRunID, result.RunID)
	}
	if workflow.sawTask != "workflow-task" || workflow.sawTarget != "target-1" || workflow.sawWall != time.Minute {
		t.Fatalf("workflow saw task=%q target=%q wall=%s", workflow.sawTask, workflow.sawTarget, workflow.sawWall)
	}
}

type duplicateOpenWorkflow struct {
	errs chan error
}

func (w *duplicateOpenWorkflow) Execute(ctx context.Context, rt harness.WorkflowRuntime) (harness.WorkflowResult, error) {
	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			_, err := rt.OpenSession(ctx, harness.AgentSpec{ID: "agent-1"})
			w.errs <- err
		}()
	}
	wg.Wait()
	close(w.errs)
	return harness.WorkflowResult{}, nil
}

func TestWorkflowRuntimeRejectsDuplicateSessionID(t *testing.T) {
	store := newMemoryStore()
	factory := &fakeFactory{newSession: func(harness.SessionRequest) harness.AgentSession {
		return &fakeSession{result: harness.AgentResult{Status: harness.StatusSucceeded}}
	}}
	runner, err := harness.NewRunner(harness.Config{Store: store, DefaultFactory: factory, WorkspaceRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	workflow := &duplicateOpenWorkflow{errs: make(chan error, 2)}
	result, err := runner.Run(context.Background(), harness.RunSpec{
		Task:     harness.Task{ID: "duplicate", Objective: "solve"},
		Agents:   []harness.AgentSpec{{ID: "agent-1"}},
		Workflow: workflow,
		Verifier: fakeVerifier{verdict: harness.Verdict{Success: true}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != harness.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", result.Status)
	}
	var success, duplicate int
	for openErr := range workflow.errs {
		if openErr == nil {
			success++
			continue
		}
		if strings.Contains(openErr.Error(), "already") {
			duplicate++
		}
	}
	if success != 1 || duplicate != 1 {
		t.Fatalf("open outcomes success=%d duplicate=%d", success, duplicate)
	}
}

type twoSessionWorkflow struct {
	secondErr error
}

func (w *twoSessionWorkflow) Execute(ctx context.Context, rt harness.WorkflowRuntime) (harness.WorkflowResult, error) {
	if _, err := rt.OpenSession(ctx, harness.AgentSpec{ID: "agent-1"}); err != nil {
		return harness.WorkflowResult{}, err
	}
	_, w.secondErr = rt.OpenSession(ctx, harness.AgentSpec{ID: "agent-2"})
	return harness.WorkflowResult{}, nil
}

func TestWorkflowRuntimeEnforcesMaxSessions(t *testing.T) {
	store := newMemoryStore()
	factory := &fakeFactory{newSession: func(harness.SessionRequest) harness.AgentSession {
		return &fakeSession{result: harness.AgentResult{Status: harness.StatusSucceeded}}
	}}
	runner, err := harness.NewRunner(harness.Config{
		Store: store, DefaultFactory: factory, WorkspaceRoot: t.TempDir(), MaxSessions: 1,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	workflow := &twoSessionWorkflow{}
	result, err := runner.Run(context.Background(), harness.RunSpec{
		Task:     harness.Task{ID: "limit", Objective: "solve"},
		Agents:   []harness.AgentSpec{{ID: "agent-1"}, {ID: "agent-2"}},
		Workflow: workflow,
		Verifier: fakeVerifier{verdict: harness.Verdict{Success: true}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if workflow.secondErr == nil || !strings.Contains(workflow.secondErr.Error(), "maximum") {
		t.Fatalf("second open error = %v", workflow.secondErr)
	}
	if len(result.Sessions) != 1 || result.Sessions[0].ID != "agent-1" {
		t.Fatalf("sessions = %#v", result.Sessions)
	}
}

type eventFailStore struct {
	*memoryStore
	failType string
}

func (s *eventFailStore) AppendEvent(ctx context.Context, event harness.Event) (harness.Event, error) {
	if event.Type == s.failType {
		return harness.Event{}, errors.New("event store unavailable")
	}
	return s.memoryStore.AppendEvent(ctx, event)
}

type eventErrorWorkflow struct{}

func (eventErrorWorkflow) Execute(ctx context.Context, rt harness.WorkflowRuntime) (harness.WorkflowResult, error) {
	lease, err := rt.OpenSession(ctx, rt.Agents()[0])
	if err != nil {
		return harness.WorkflowResult{}, err
	}
	return harness.WorkflowResult{}, rt.Emit(ctx, harness.Event{
		SessionID: lease.ID,
		Source:    "control",
		Type:      "workflow.observed",
	})
}

func TestWorkflowEventPersistenceFailureFailsRun(t *testing.T) {
	store := &eventFailStore{memoryStore: newMemoryStore(), failType: "workflow.observed"}
	factory := &fakeFactory{newSession: func(harness.SessionRequest) harness.AgentSession {
		return &fakeSession{result: harness.AgentResult{Status: harness.StatusSucceeded}}
	}}
	runner, err := harness.NewRunner(harness.Config{Store: store, DefaultFactory: factory, WorkspaceRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	result, err := runner.Run(context.Background(), harness.RunSpec{
		Task:     harness.Task{ID: "event-failure", Objective: "solve"},
		Agents:   []harness.AgentSpec{{ID: "agent-1"}},
		Workflow: eventErrorWorkflow{},
		Verifier: fakeVerifier{verdict: harness.Verdict{Success: true}},
	})
	if err == nil {
		t.Fatal("expected event persistence error")
	}
	if result.Status != harness.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Verdict != nil && result.Verdict.Success {
		t.Fatalf("success verdict after persistence failure: %#v", result.Verdict)
	}
}

type directEventFailStore struct {
	*memoryStore
}

func (s *directEventFailStore) AppendEvent(ctx context.Context, event harness.Event) (harness.Event, error) {
	if event.Type == "test.event" {
		return harness.Event{}, errors.New("direct event store unavailable")
	}
	return s.memoryStore.AppendEvent(ctx, event)
}

func TestDirectWorkflowAgentEventPersistenceFailureFailsRun(t *testing.T) {
	store := &directEventFailStore{memoryStore: newMemoryStore()}
	factory := &fakeFactory{newSession: func(harness.SessionRequest) harness.AgentSession {
		return &fakeSession{
			result: harness.AgentResult{Status: harness.StatusSucceeded},
			events: [][]byte{[]byte(`{"text":"observation"}`)},
		}
	}}
	runner, err := harness.NewRunner(harness.Config{Store: store, DefaultFactory: factory, WorkspaceRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	result, err := runner.Run(context.Background(), harness.RunSpec{
		Task:     harness.Task{ID: "direct-event-failure", Objective: "solve"},
		Agents:   []harness.AgentSpec{{ID: "agent-1"}},
		Verifier: fakeVerifier{verdict: harness.Verdict{Success: true}},
	})
	if err == nil || !strings.Contains(err.Error(), "event store unavailable") {
		t.Fatalf("Run error = %v, result=%#v", err, result)
	}
	if result.Status != harness.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
}
