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

func TestServerAcceptsV3Descriptor(t *testing.T) {
	store, runner := testServerRunner(t)
	defer store.Close()
	defer runner.Close(context.Background())
	builder := func(descriptor harness.RunDescriptor) (harness.RunSpec, error) {
		spec := harness.RunSpec{
			Task: descriptor.Task, Target: descriptor.Target,
			Reasoner: harness.AgentSpec{ID: descriptor.Reasoner.ID, Runtime: noopRuntime{}},
			Executor: harness.AgentSpec{ID: descriptor.Executor.ID, Runtime: noopRuntime{}},
			Policy:   descriptor.Policy, Artifacts: store, Verifier: fakeVerifier{},
			ReasonStrategy: noIntentReasonStrategy{},
		}
		return spec, harness.ValidateRunSpec(spec)
	}
	handler := server.New(runner, store, builder).Handler()
	body := `{"schema_version":3,"task":{"id":"task","objective":"x"},"target":{"id":"target","isolation":"caller_managed"},"reasoner":{"id":"reasoner"},"executor":{"id":"exec"},"policy":{"budget":{"wall_time":1000000000},"exploration":{"max_open_intents":1}}}`
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
	cancelReq := httptest.NewRequest(http.MethodPost, "/v1/runs/"+accepted.RunID+"/cancel", nil)
	cancelRec := httptest.NewRecorder()
	handler.ServeHTTP(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusAccepted {
		t.Fatalf("cancel status = %d body=%s", cancelRec.Code, cancelRec.Body.String())
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		record, err := store.GetRun(context.Background(), accepted.RunID)
		if err == nil && record.Phase == harness.RunPhaseTerminal {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not settle after cancel: %+v err=%v", record, err)
		}
		time.Sleep(10 * time.Millisecond)
	}

}

func TestServerRejectsUnknownFieldsAndV1Descriptor(t *testing.T) {
	store, runner := testServerRunner(t)
	handler := server.New(runner, store, func(descriptor harness.RunDescriptor) (harness.RunSpec, error) {
		return harness.RunSpec{Task: descriptor.Task, Target: descriptor.Target, Artifacts: store, Verifier: fakeVerifier{}}, nil
	}).Handler()
	for _, body := range []string{
		`{"schema_version":3,"unknown":true}`,
		`{"schema_version":1,"task":{"id":"task","objective":"x"},"target":{"id":"target"},"planner":{"id":"planner"},"executors":[{"id":"exec"}]}`,
		`{"schema_version":3,"task":{"id":"task","objective":"x"},"target":{"id":"target","isolation":"network_isolated"},"reasoner":{"id":"reasoner"},"executor":{"id":"exec"}}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/v1/runs", strings.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, rec.Code, rec.Body.String())
		}
	}
}

func TestSSEReplaysAfterSequence(t *testing.T) {
	store, runner := testServerRunner(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.CreateRun(ctx, harness.RunRecord{ID: "run-1", Phase: harness.RunPhaseQueued, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	first, _ := store.AppendEvent(ctx, harness.Event{RunID: "run-1", Source: "test", Type: harness.EventAgent, Time: now, Data: json.RawMessage(`{"n":1}`)})
	second, _ := store.AppendEvent(ctx, harness.Event{RunID: "run-1", Source: "test", Type: harness.EventAgent, Time: now, Data: json.RawMessage(`{"n":2}`)})
	handler := server.New(runner, store, func(harness.RunDescriptor) (harness.RunSpec, error) { return harness.RunSpec{}, nil }).Handler()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/run-1/events?after_seq=1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	body := rec.Body.String()
	if strings.Contains(body, `"seq":1`) || !strings.Contains(body, `"seq":2`) {
		t.Fatalf("replay body = %s first=%d second=%d", body, first.Seq, second.Seq)
	}
}

func TestGraphEndpointReturnsClaimIntentView(t *testing.T) {
	store, runner := testServerRunner(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.CreateRun(ctx, harness.RunRecord{ID: "run-graph", Phase: harness.RunPhaseRunning, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveClaim(ctx, harness.Claim{ID: "claim-1", RunID: "run-graph", TaskID: "task", Kind: harness.ClaimNegative, Description: "none", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveIntent(ctx, harness.Intent{ID: "intent-1", RunID: "run-graph", Kind: harness.IntentExplore, FromClaimIDs: []string{"claim-1"}, Description: "probe", State: harness.IntentClaimed, Slot: 1, ExecutorID: "executor-1", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHint(ctx, harness.Hint{ID: "hint-1", RunID: "run-graph", Content: "focus", Creator: "operator", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	reasonEvent, err := harness.NewEvent("run-graph", harness.EventReasonStarted, harness.ReasonPayload{FrontierVersion: 4, FreeSlots: []int{2}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(ctx, reasonEvent); err != nil {
		t.Fatal(err)
	}
	handler := server.New(runner, store, func(harness.RunDescriptor) (harness.RunSpec, error) { return harness.RunSpec{}, nil }).Handler()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/run-graph/board", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var graph struct {
		Claims        []harness.Claim  `json:"claims"`
		Intents       []harness.Intent `json:"intents"`
		Hints         []harness.Hint   `json:"hints"`
		Slots         []int            `json:"slots"`
		BoardRevision uint64           `json:"board_revision"`
		Reason        struct {
			Active          bool   `json:"active"`
			FrontierVersion uint64 `json:"frontier_version"`
		} `json:"reason"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &graph); err != nil {
		t.Fatal(err)
	}
	if len(graph.Claims) != 1 || len(graph.Intents) != 1 || len(graph.Hints) != 1 || len(graph.Slots) != 0 {
		t.Fatalf("graph = %+v", graph)
	}
	if !graph.Reason.Active || graph.Reason.FrontierVersion != 4 {
		t.Fatalf("reason projection = %+v", graph.Reason)
	}
}

func testServerRunner(t *testing.T) (*sqlite.Store, *harness.Runner) {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	runner, err := harness.NewRunner(harness.Config{Store: store, Starter: noopStarter{}, WorkspaceRoot: t.TempDir(), SkipRecovery: true})
	if err != nil {
		t.Fatal(err)
	}
	return store, runner
}

type noopRuntime struct{}

func (noopRuntime) NewSession(context.Context, harness.RuntimeRequest) (harness.AgentSession, error) {
	return noopSession{}, nil
}

type noopSession struct{}

func (noopSession) Prompt(ctx context.Context, _ string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return nil
}
func (noopSession) Steer(context.Context, string) error { return nil }
func (noopSession) Reset(context.Context) error         { return nil }
func (noopSession) Events() <-chan harness.AgentEvent {
	c := make(chan harness.AgentEvent, 2)
	c <- harness.AgentEvent{Kind: harness.AgentEventMessage, Text: `{"phase":"concluded","claim":{"kind":"negative","description":"none"}}`}
	c <- harness.AgentEvent{Kind: harness.AgentEventSettled}
	return c
}
func (noopSession) Logs() <-chan harness.LogEvent { return nil }
func (noopSession) Usage() harness.Usage          { return harness.Usage{} }
func (noopSession) Abort(context.Context) error   { return nil }
func (noopSession) Close(context.Context) error   { return nil }

type fakeVerifier struct{}

func (fakeVerifier) Verify(context.Context, harness.VerifyRequest) (harness.Verdict, error) {
	return harness.Verdict{Success: false}, nil
}

type noIntentReasonStrategy struct{}

func (noIntentReasonStrategy) Prompt(harness.ReasonRequest) (string, error) { return "reason", nil }
func (noIntentReasonStrategy) Parse(req harness.ReasonRequest, _ harness.TurnResult) (harness.ReasonReport, error) {
	return harness.ReasonReport{FrontierVersion: req.FrontierVersion}, nil
}

type noIntentSweepReasonStrategy struct{}

func (noIntentSweepReasonStrategy) Prompt(harness.ReasonRequest) (string, error) {
	return "reason", nil
}
func (noIntentSweepReasonStrategy) Parse(req harness.ReasonRequest, _ harness.TurnResult) (harness.ReasonReport, error) {
	return harness.ReasonReport{FrontierVersion: req.FrontierVersion}, nil
}

type sweepOnlyRuntime struct{}

func (sweepOnlyRuntime) NewSession(_ context.Context, req harness.RuntimeRequest) (harness.AgentSession, error) {
	if req.Role == harness.AgentRoleReasoner {
		return newFixedSession("reason"), nil
	}
	return newFixedSession(`{"phase":"concluded","claim":{"kind":"negative","description":"none"}}`), nil
}

type fixedSession struct {
	events chan harness.AgentEvent
	text   string
}

func newFixedSession(text string) *fixedSession {
	return &fixedSession{events: make(chan harness.AgentEvent, 4), text: text}
}

func (s *fixedSession) Prompt(ctx context.Context, _ string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	s.events <- harness.AgentEvent{Kind: harness.AgentEventMessage, Text: s.text}
	s.events <- harness.AgentEvent{Kind: harness.AgentEventSettled}
	return nil
}
func (*fixedSession) Steer(context.Context, string) error { return nil }
func (*fixedSession) Reset(context.Context) error         { return nil }
func (s *fixedSession) Events() <-chan harness.AgentEvent { return s.events }
func (*fixedSession) Logs() <-chan harness.LogEvent       { return nil }
func (*fixedSession) Usage() harness.Usage                { return harness.Usage{} }
func (*fixedSession) Abort(context.Context) error         { return nil }
func (*fixedSession) Close(context.Context) error         { return nil }

func TestServerTSecSweepUsesV2RunEngine(t *testing.T) {
	store, runner := testServerRunner(t)
	defer store.Close()
	defer runner.Close(context.Background())
	builder := func(descriptor harness.RunDescriptor) (harness.RunSpec, error) {
		return makeTestRunSpec(descriptor, store), nil
	}
	sweepBuilder := func(req server.SweepRequest) (server.SweepPlan, error) {
		if req.Descriptor.Executor.ID == "" || req.Challenges[0] != "web_1" {
			return server.SweepPlan{}, context.Canceled
		}
		return server.SweepPlan{
			ChallengeCodes: req.Challenges,
			Parallel:       1,
			Build: func(code string) (harness.RunSpec, error) {
				descriptor := req.Descriptor
				descriptor.Task.ID = "task-" + code
				return makeTestRunSpec(descriptor, store), nil
			},
		}, nil
	}
	handler := server.New(runner, store, builder, sweepBuilder).Handler()
	body := `{"descriptor":{"schema_version":3,"task":{"id":"task","objective":"x"},"target":{"id":"target","isolation":"caller_managed"},"reasoner":{"id":"reasoner"},"executor":{"id":"exec"}},"tsec":{"base_url":"https://example.test"},"challenges":["web_1"],"parallel":1}`
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/tsec/sweeps", strings.NewReader(body)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create sweep status = %d body=%s", rec.Code, rec.Body.String())
	}
	var sweep server.SweepState
	if err := json.Unmarshal(rec.Body.Bytes(), &sweep); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		getRec := httptest.NewRecorder()
		handler.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/v1/tsec/sweeps/"+sweep.ID, nil))
		if getRec.Code != http.StatusOK {
			t.Fatalf("get sweep status = %d body=%s", getRec.Code, getRec.Body.String())
		}
		if err := json.Unmarshal(getRec.Body.Bytes(), &sweep); err != nil {
			t.Fatal(err)
		}
		if sweep.Status == harness.StatusSucceeded || sweep.Status == harness.StatusFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sweep did not finish: %+v", sweep)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if sweep.Status != harness.StatusSucceeded || len(sweep.RunIDs) != 1 || len(sweep.Results) != 1 {
		t.Fatalf("sweep result = %+v", sweep)
	}
	record, err := store.GetRun(context.Background(), sweep.RunIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if record.Phase != harness.RunPhaseTerminal || record.Outcome != harness.RunOutcomeFailed {
		t.Fatalf("sweep run did not use v2 terminal state: %+v", record)
	}
}

func makeTestRunSpec(descriptor harness.RunDescriptor, store harness.ArtifactStore) harness.RunSpec {
	return harness.RunSpec{
		Task: descriptor.Task, Target: descriptor.Target,
		Reasoner: harness.AgentSpec{ID: descriptor.Reasoner.ID, Runtime: sweepOnlyRuntime{}},
		Executor: harness.AgentSpec{ID: descriptor.Executor.ID, Runtime: sweepOnlyRuntime{}},
		Policy:   descriptor.Policy, Artifacts: store, Verifier: fakeVerifier{},
		ReasonStrategy: noIntentSweepReasonStrategy{},
	}
}
