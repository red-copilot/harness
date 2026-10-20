package sqlite_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/store/sqlite"
)

func TestStorePersistsOrderedEventsLogsAndInterruption(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "harness.db")
	store, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("database mode = %o, want 600", got)
	}

	now := time.Now().UTC()
	run := harness.RunRecord{
		ID:        "run_1",
		Status:    harness.StatusRunning,
		Spec:      json.RawMessage(`{"task":{"id":"demo"}}`),
		CreatedAt: now,
		StartedAt: &now,
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	got, err := store.GetRun(ctx, "run_1")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.Status != harness.StatusRunning || string(got.Spec) != string(run.Spec) {
		t.Fatalf("unexpected run: %#v", got)
	}

	first, err := store.AppendEvent(ctx, harness.Event{
		RunID: "run_1", Type: "run.started", Source: "harness",
		Time: now, Data: json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("AppendEvent 1: %v", err)
	}
	second, err := store.AppendEvent(ctx, harness.Event{
		RunID: "run_1", SessionID: "s1", Type: "rpc.event", Source: "pi",
		Time: now.Add(time.Second), Data: json.RawMessage(`{"raw_type":"agent_start"}`),
	})
	if err != nil {
		t.Fatalf("AppendEvent 2: %v", err)
	}
	if first.Seq == 0 || second.Seq <= first.Seq {
		t.Fatalf("event sequence not increasing: %d then %d", first.Seq, second.Seq)
	}

	events, err := store.ListEvents(ctx, "run_1", first.Seq)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Seq != second.Seq || events[0].Type != "rpc.event" {
		t.Fatalf("unexpected events: %#v", events)
	}

	if _, err := store.AppendLog(ctx, harness.LogRecord{
		RunID: "run_1", SessionID: "s1", Stream: "stderr", Time: now, Text: "diagnostic\n",
	}); err != nil {
		t.Fatalf("AppendLog: %v", err)
	}
	logs, err := store.ListLogs(ctx, "run_1", 0)
	if err != nil {
		t.Fatalf("ListLogs: %v", err)
	}
	if len(logs) != 1 || logs[0].Text != "diagnostic\n" {
		t.Fatalf("unexpected logs: %#v", logs)
	}

	if err := store.SaveVerdict(ctx, harness.VerdictRecord{
		RunID: "run_1", Success: true, Score: 10, Message: "verified",
		Evidence: json.RawMessage(`[{"kind":"flag"}]`), CreatedAt: now,
	}); err != nil {
		t.Fatalf("SaveVerdict: %v", err)
	}
	verdict, err := store.GetVerdict(ctx, "run_1")
	if err != nil {
		t.Fatalf("GetVerdict: %v", err)
	}
	if !verdict.Success || verdict.Score != 10 {
		t.Fatalf("unexpected verdict: %#v", verdict)
	}

	if err := store.SaveSweep(ctx, harness.SweepRecord{
		ID: "sweep_1", Status: harness.StatusRunning, Result: json.RawMessage(`{"run_ids":["run_1"]}`),
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("SaveSweep: %v", err)
	}
	sweep, err := store.GetSweep(ctx, "sweep_1")
	if err != nil {
		t.Fatalf("GetSweep: %v", err)
	}
	if sweep.Status != harness.StatusRunning || string(sweep.Result) != `{"run_ids":["run_1"]}` {
		t.Fatalf("unexpected sweep: %#v", sweep)
	}
	sweeps, err := store.ListSweeps(ctx, 10)
	if err != nil || len(sweeps) != 1 {
		t.Fatalf("ListSweeps: %#v, %v", sweeps, err)
	}

	if err := store.MarkInterrupted(ctx); err != nil {
		t.Fatalf("MarkInterrupted: %v", err)
	}
	got, err = store.GetRun(ctx, "run_1")
	if err != nil {
		t.Fatalf("GetRun after interrupt: %v", err)
	}
	if got.Status != harness.StatusInterrupted {
		t.Fatalf("status = %q, want interrupted", got.Status)
	}
}
