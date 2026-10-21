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

func newObservabilityHandler(t *testing.T) (http.Handler, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return server.New(nil, store, nil).Handler(), store
}

func seedRun(t *testing.T, store *sqlite.Store, record harness.RunRecord) {
	t.Helper()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	if err := store.CreateRun(context.Background(), record); err != nil {
		t.Fatal(err)
	}
}

func seedLogs(t *testing.T, store *sqlite.Store, runID string, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		if _, err := store.AppendLog(context.Background(), harness.LogRecord{
			RunID: runID, Stream: "stdout", Text: "line-" + string(rune('a'+i)), Time: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestServerLogsTailAndCursor(t *testing.T) {
	handler, store := newObservabilityHandler(t)
	seedRun(t, store, harness.RunRecord{ID: "run_logs", Status: harness.StatusSucceeded})
	seedLogs(t, store, "run_logs", 5)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/runs/run_logs/logs?tail=true&limit=3", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("tail status = %d body=%s", rec.Code, rec.Body.String())
	}
	var tail []harness.LogRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &tail); err != nil {
		t.Fatal(err)
	}
	if len(tail) != 3 || tail[0].Seq != 3 || tail[2].Seq != 5 {
		t.Fatalf("unexpected tail: %+v", tail)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/runs/run_logs/logs?after_seq=3&limit=2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("cursor status = %d body=%s", rec.Code, rec.Body.String())
	}
	var page []harness.LogRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].Seq != 4 || page[1].Seq != 5 {
		t.Fatalf("unexpected page: %+v", page)
	}
}

func TestServerLogsValidationAndMissingRun(t *testing.T) {
	handler, store := newObservabilityHandler(t)
	seedRun(t, store, harness.RunRecord{ID: "run_logs", Status: harness.StatusSucceeded})

	for _, path := range []string{
		"/v1/runs/run_logs/logs?after_seq=-1",
		"/v1/runs/run_logs/logs?limit=0",
		"/v1/runs/run_logs/logs?limit=5001",
		"/v1/runs/run_logs/logs?tail=maybe",
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d body=%s", path, rec.Code, rec.Body.String())
		}
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/runs/missing/logs", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing run status = %d body=%s", rec.Code, rec.Body.String())
	}

	for _, path := range []string{"/v1/runs/run_logs/logs", "/v1/runs/run_logs/verdict"} {
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s method status = %d body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestServerVerdict(t *testing.T) {
	handler, store := newObservabilityHandler(t)
	seedRun(t, store, harness.RunRecord{ID: "run_verdict", Status: harness.StatusSucceeded})
	if err := store.SaveVerdict(context.Background(), harness.VerdictRecord{
		RunID: "run_verdict", Success: true, Score: 4.5, Message: "flag accepted",
		Evidence: json.RawMessage(`{"flag":"ok"}`), CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/runs/run_verdict/verdict", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("verdict status = %d body=%s", rec.Code, rec.Body.String())
	}
	var verdict harness.VerdictRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &verdict); err != nil {
		t.Fatal(err)
	}
	if !verdict.Success || verdict.Score != 4.5 || verdict.Message != "flag accepted" {
		t.Fatalf("unexpected verdict: %+v", verdict)
	}

	seedRun(t, store, harness.RunRecord{ID: "run_no_verdict", Status: harness.StatusFailed})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/runs/run_no_verdict/verdict", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing verdict status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestServerOverview(t *testing.T) {
	handler, store := newObservabilityHandler(t)
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	seedRun(t, store, harness.RunRecord{
		ID: "run_ok", Status: harness.StatusSucceeded,
		Result:    mustJSON(t, harness.Result{Usage: harness.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15, CostUSD: 0.1}}),
		CreatedAt: base, StartedAt: timePtr(base), FinishedAt: timePtr(base.Add(time.Second)),
	})
	seedRun(t, store, harness.RunRecord{
		ID: "run_fail", Status: harness.StatusFailed,
		Result:    mustJSON(t, harness.Result{Usage: harness.Usage{InputTokens: 20, OutputTokens: 10, TotalTokens: 30, CostUSD: 0.2}}),
		CreatedAt: base.Add(time.Minute), StartedAt: timePtr(base.Add(time.Second)), FinishedAt: timePtr(base.Add(3 * time.Second)),
	})
	seedRun(t, store, harness.RunRecord{
		ID: "run_cancel", Status: harness.StatusCancelled,
		CreatedAt: base.Add(2 * time.Minute), StartedAt: timePtr(base.Add(2 * time.Second)), FinishedAt: timePtr(base.Add(5 * time.Second)),
	})
	seedRun(t, store, harness.RunRecord{
		ID: "run_live", Status: harness.StatusRunning,
		CreatedAt: base.Add(3 * time.Minute), StartedAt: timePtr(base.Add(3 * time.Minute)),
	})
	seedRun(t, store, harness.RunRecord{
		ID: "run_bad_result", Status: harness.StatusFailed, Result: json.RawMessage(`{`),
		CreatedAt: base.Add(4 * time.Minute),
	})

	for _, record := range []harness.SweepRecord{
		{ID: "sweep_live", Status: harness.StatusRunning, CreatedAt: base},
		{ID: "sweep_ok", Status: harness.StatusSucceeded, CreatedAt: base},
		{ID: "sweep_fail", Status: harness.StatusFailed, CreatedAt: base},
	} {
		if err := store.SaveSweep(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/overview", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("overview status = %d body=%s", rec.Code, rec.Body.String())
	}
	var overview server.Overview
	if err := json.Unmarshal(rec.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	if overview.Window != 100 || overview.Runs.Total != 5 || overview.Runs.Active != 1 || overview.Runs.Terminal != 4 {
		t.Fatalf("unexpected run summary: %+v", overview.Runs)
	}
	if overview.Runs.Succeeded != 1 || overview.Runs.Failed != 2 || overview.Runs.Cancelled != 1 || overview.Runs.SuccessRate != 0.25 {
		t.Fatalf("unexpected status counts: %+v", overview.Runs)
	}
	if overview.Usage.TotalTokens != 45 || overview.Usage.InputTokens != 30 || overview.Usage.OutputTokens != 15 {
		t.Fatalf("unexpected usage: %+v", overview.Usage)
	}
	if overview.Duration.Samples != 3 || overview.Duration.AverageMS != 2000 || overview.Duration.P50MS != 2000 || overview.Duration.P95MS != 3000 {
		t.Fatalf("unexpected duration: %+v", overview.Duration)
	}
	if overview.Sweeps.Total != 3 || overview.Sweeps.Active != 1 || overview.Sweeps.Succeeded != 1 || overview.Sweeps.Failed != 1 {
		t.Fatalf("unexpected sweeps: %+v", overview.Sweeps)
	}
}

func TestServerSSELastEventID(t *testing.T) {
	handler, store := newObservabilityHandler(t)
	seedRun(t, store, harness.RunRecord{ID: "run_sse", Status: harness.StatusSucceeded})
	for _, eventType := range []string{"run.started", "session.started", "run.finished"} {
		if _, err := store.AppendEvent(context.Background(), harness.Event{
			RunID: "run_sse", Source: "harness", Type: eventType, Time: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/runs/run_sse/events", nil)
	req.Header.Set("Last-Event-ID", "1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sse status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "run.started") || !strings.Contains(body, "session.started") || !strings.Contains(body, "run.finished") {
		t.Fatalf("unexpected sse replay: %s", body)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/runs/run_sse/events?after_seq=2", nil)
	req.Header.Set("Last-Event-ID", "0")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	body = rec.Body.String()
	if strings.Contains(body, "session.started") || !strings.Contains(body, "run.finished") {
		t.Fatalf("query after_seq should win: %s", body)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func timePtr(value time.Time) *time.Time { return &value }
