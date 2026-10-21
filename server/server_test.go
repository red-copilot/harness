package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/server"
	"github.com/red-copilot/harness/store/sqlite"
)

func TestServerAsyncRunStatusAndSSE(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	factory := &fakeFactory{session: &fakeSession{result: harness.AgentResult{Status: harness.StatusSucceeded}}}
	runner, err := harness.NewRunner(harness.Config{
		Store: store, DefaultFactory: factory, WorkspaceRoot: t.TempDir(),
		PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := server.New(runner, store, func(req server.RunRequest) (harness.RunSpec, error) {
		spec := req.Run
		spec.Verifier = fakeVerifier{harness.Verdict{Success: true, Score: 3}}
		return spec, nil
	}).Handler()

	body := `{"run":{"task":{"id":"demo","objective":"solve"},"agents":[{"id":"a"}]}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.RunID == "" {
		t.Fatalf("create response: %v %s", err, rec.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/runs/"+created.RunID, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("get status = %d body=%s", rec.Code, rec.Body.String())
		}
		var record harness.RunRecord
		_ = json.Unmarshal(rec.Body.Bytes(), &record)
		if record.Status == harness.StatusSucceeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not finish: %s", rec.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/runs/"+created.RunID+"/events", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("events status = %d body=%s", rec.Code, rec.Body.String())
	}
	scanner := bufio.NewScanner(bytes.NewReader(rec.Body.Bytes()))
	var sawFinished bool
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "run.finished") {
			sawFinished = true
		}
	}
	if !sawFinished {
		t.Fatalf("SSE did not include run.finished: %s", rec.Body.String())
	}
}

func TestServerRejectsInvalidRun(t *testing.T) {
	store, _ := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	runner, _ := harness.NewRunner(harness.Config{Store: store, WorkspaceRoot: t.TempDir()})
	handler := server.New(runner, store, nil).Handler()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/runs",
		strings.NewReader(`{"run":{"task":{"id":"demo"}}}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

type fakeFactory struct {
	mu      sync.Mutex
	session harness.AgentSession
}

func (f *fakeFactory) NewSession(context.Context, harness.SessionRequest) (harness.AgentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.session, nil
}

type fakeSession struct {
	result harness.AgentResult
}

func (s *fakeSession) Prompt(context.Context, string) error { return nil }
func (s *fakeSession) Steer(context.Context, string) error  { return nil }
func (s *fakeSession) Events() <-chan harness.AgentEvent {
	ch := make(chan harness.AgentEvent)
	close(ch)
	return ch
}
func (s *fakeSession) Wait(context.Context) (harness.AgentResult, error) { return s.result, nil }
func (s *fakeSession) Abort(context.Context) error                       { return nil }
func (s *fakeSession) Close(context.Context) error                       { return nil }

type fakeVerifier struct{ verdict harness.Verdict }

func (v fakeVerifier) Verify(context.Context, harness.VerifyRequest) (harness.Verdict, error) {
	return v.verdict, nil
}

func TestServerTSecSweep(t *testing.T) {
	store, _ := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	factory := &fakeFactory{session: &fakeSession{result: harness.AgentResult{Status: harness.StatusSucceeded}}}
	runner, _ := harness.NewRunner(harness.Config{Store: store, DefaultFactory: factory, WorkspaceRoot: t.TempDir()})
	builder := func(req server.RunRequest) (harness.RunSpec, error) { return req.Run, nil }
	sweepBuilder := func(req server.SweepRequest) (server.SweepPlan, error) {
		return server.SweepPlan{ChallengeCodes: []string{"a", "b"}, Parallel: 2, Build: func(code string) (harness.RunSpec, error) {
			spec := req.Run
			spec.Task.ID = "tsec-" + code
			spec.Verifier = fakeVerifier{harness.Verdict{Success: true}}
			return spec, nil
		}}, nil
	}
	handler := server.New(runner, store, builder, sweepBuilder).Handler()
	body := `{"run":{"task":{"id":"base","objective":"solve"},"agents":[{"id":"a"}]},"challenges":["a","b"]}`
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/tsec/sweeps", strings.NewReader(body)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create sweep status = %d body=%s", rec.Code, rec.Body.String())
	}
	var state server.SweepState
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil || state.ID == "" {
		t.Fatalf("sweep response: %v %s", err, rec.Body.String())
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/tsec/sweeps/"+state.ID, nil))
		var got server.SweepState
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		if got.Status == harness.StatusSucceeded && len(got.Results) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sweep did not finish: %s", rec.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	reloaded := server.New(runner, store, builder, sweepBuilder).Handler()
	rec = httptest.NewRecorder()
	reloaded.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/tsec/sweeps/"+state.ID, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), state.ID) {
		t.Fatalf("persisted sweep: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestServerPassesExplorationPolicyToBuilder(t *testing.T) {
	store, _ := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	factory := &fakeFactory{session: &fakeSession{result: harness.AgentResult{Status: harness.StatusSucceeded}}}
	runner, _ := harness.NewRunner(harness.Config{Store: store, DefaultFactory: factory, WorkspaceRoot: t.TempDir()})
	var gotDepth int
	builder := func(req server.RunRequest) (harness.RunSpec, error) {
		if req.Exploration == nil {
			t.Fatal("exploration policy was not decoded")
		}
		gotDepth = req.Exploration.MaxDepth
		spec := req.Run
		spec.Verifier = fakeVerifier{verdict: harness.Verdict{Success: true}}
		return spec, nil
	}
	handler := server.New(runner, store, builder).Handler()
	body := `{"run":{"task":{"id":"explore","objective":"solve"},"agents":[{"id":"a"}]},"exploration":{"max_depth":2}}`
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/runs", strings.NewReader(body)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if gotDepth != 2 {
		t.Fatalf("exploration max_depth = %d, want 2", gotDepth)
	}
}

func TestServerAcceptsLegacyNestedExplorationPolicy(t *testing.T) {
	store, _ := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	factory := &fakeFactory{session: &fakeSession{result: harness.AgentResult{Status: harness.StatusSucceeded}}}
	runner, _ := harness.NewRunner(harness.Config{Store: store, DefaultFactory: factory, WorkspaceRoot: t.TempDir()})
	var gotDepth int
	builder := func(req server.RunRequest) (harness.RunSpec, error) {
		if req.Exploration == nil {
			t.Fatal("nested exploration policy was not decoded")
		}
		gotDepth = req.Exploration.MaxDepth
		spec := req.Run
		spec.Verifier = fakeVerifier{verdict: harness.Verdict{Success: true}}
		return spec, nil
	}
	handler := server.New(runner, store, builder).Handler()
	body := `{"run":{"task":{"id":"explore","objective":"solve"},"agents":[{"id":"a"}],"exploration":{"max_depth":3}}}`
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/runs", strings.NewReader(body)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if gotDepth != 3 {
		t.Fatalf("exploration max_depth = %d, want 3", gotDepth)
	}
}
