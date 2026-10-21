package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/server"
	"github.com/red-copilot/harness/store/sqlite"
)

func TestServerAcceptsRunDescriptor(t *testing.T) {
	store, runner := testServerRunner(t)
	builder := func(descriptor harness.RunDescriptor) (harness.RunSpec, error) {
		descriptor.Planner.Runtime = noopRuntime{}
		for index := range descriptor.Executors {
			descriptor.Executors[index].Runtime = noopRuntime{}
		}
		return harness.RunSpec{Task: descriptor.Task, Target: descriptor.Target, Planner: descriptor.Planner, Executors: descriptor.Executors, Policy: descriptor.Policy, Artifacts: store, Verifier: fakeVerifier{}, PlannerStrategy: emptyPlannerStrategy{}, ExecutorStrategy: emptyExecutorStrategy{}}, nil
	}
	handler := server.New(runner, store, builder).Handler()
	body := `{"schema_version":1,"task":{"id":"task","objective":"x"},"target":{"id":"target","isolation":"caller_managed"},"planner":{"id":"planner","runtime":{"tools":["read"]}},"executors":[{"id":"exec"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var accepted struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		record, err := store.GetRun(context.Background(), accepted.RunID)
		if err == nil && (record.Status == harness.StatusSucceeded || record.Status == harness.StatusFailed || record.Status == harness.StatusCancelled || record.Status == harness.StatusStagnated || record.Status == harness.StatusInterrupted || record.Status == harness.StatusTimedOut) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s did not settle: %+v err=%v", accepted.RunID, record, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestServerRejectsUnknownFieldsAndUnsupportedIsolation(t *testing.T) {
	store, runner := testServerRunner(t)
	handler := server.New(runner, store, func(descriptor harness.RunDescriptor) (harness.RunSpec, error) {
		descriptor.Planner.Runtime = noopRuntime{}
		for index := range descriptor.Executors {
			descriptor.Executors[index].Runtime = noopRuntime{}
		}
		spec := harness.RunSpec{Task: descriptor.Task, Target: descriptor.Target, Planner: descriptor.Planner, Executors: descriptor.Executors, Artifacts: store, Verifier: fakeVerifier{}}
		return spec, harness.ValidateRunSpec(spec)
	}).Handler()
	for _, body := range []string{
		`{"schema_version":1,"unknown":true}`,
		`{"schema_version":1,"task":{"id":"task","objective":"x"},"target":{"id":"target","isolation":"network_isolated"},"planner":{"id":"planner"},"executors":[{"id":"exec"}]}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/v1/runs", strings.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d", body, rec.Code)
		}
	}
}

func TestSSEReplaysAfterSequence(t *testing.T) {
	store, runner := testServerRunner(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.CreateRun(ctx, harness.RunRecord{ID: "run-1", Status: harness.StatusFailed, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	first, _ := store.AppendEvent(ctx, harness.Event{RunID: "run-1", Source: "test", Type: "one", Time: now, Data: json.RawMessage(`{"n":1}`)})
	second, _ := store.AppendEvent(ctx, harness.Event{RunID: "run-1", Source: "test", Type: "two", Time: now, Data: json.RawMessage(`{"n":2}`)})
	handler := server.New(runner, store, func(harness.RunDescriptor) (harness.RunSpec, error) { return harness.RunSpec{}, nil }).Handler()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/run-1/events?after_seq=1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	body := rec.Body.String()
	if strings.Contains(body, `"seq":1`) || !strings.Contains(body, `"seq":2`) {
		t.Fatalf("replay body = %s first=%d second=%d", body, first.Seq, second.Seq)
	}
}

func testServerRunner(t *testing.T) (*sqlite.Store, *harness.Runner) {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	runner, err := harness.NewRunner(harness.Config{Store: store, WorkspaceRoot: t.TempDir(), SkipRecovery: true})
	if err != nil {
		t.Fatal(err)
	}
	return store, runner
}

type noopRuntime struct{}

func (noopRuntime) NewSession(context.Context, harness.RuntimeRequest) (harness.AgentSession, error) {
	return &noopSession{events: make(chan harness.AgentEvent, 1)}, nil
}

type noopSession struct{ events chan harness.AgentEvent }

func (s *noopSession) Prompt(ctx context.Context, _ string) error {
	select {
	case s.events <- harness.AgentEvent{Type: "agent_settled", Data: json.RawMessage(`{"type":"agent_settled"}`)}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (*noopSession) Steer(context.Context, string) error { return nil }
func (*noopSession) Reset(context.Context) error         { return nil }
func (s *noopSession) Events() <-chan harness.AgentEvent { return s.events }
func (*noopSession) Logs() <-chan harness.LogEvent       { return nil }
func (*noopSession) Usage() harness.Usage                { return harness.Usage{} }
func (*noopSession) Abort(context.Context) error         { return nil }
func (*noopSession) Close(context.Context) error         { return nil }

type fakeVerifier struct{}

func (fakeVerifier) Verify(context.Context, harness.VerifyRequest) (harness.Verdict, error) {
	return harness.Verdict{Success: true}, nil
}

type emptyPlannerStrategy struct{}

func (emptyPlannerStrategy) Prompt(harness.PlanRequest) (string, error) { return "plan", nil }
func (emptyPlannerStrategy) Parse(harness.PlanRequest, harness.TurnResult) (harness.PlanProposal, error) {
	return harness.PlanProposal{}, nil
}

type emptyExecutorStrategy struct{}

func (emptyExecutorStrategy) Prompt(harness.AttemptRequest) (string, error) { return "execute", nil }
func (emptyExecutorStrategy) Parse(harness.AttemptRequest, harness.TurnResult) (harness.AttemptReport, error) {
	return harness.AttemptReport{Status: harness.AttemptCompleted, Outcome: harness.AttemptInconclusive}, nil
}
