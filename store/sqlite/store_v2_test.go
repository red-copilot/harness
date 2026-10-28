package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

func TestOpenRejectsLegacyDatabaseWithoutMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE schema_meta (version INTEGER NOT NULL)`,
		`INSERT INTO schema_meta(version) VALUES (5)`,
		`CREATE TABLE runs (id TEXT PRIMARY KEY, status TEXT NOT NULL, phase TEXT NOT NULL DEFAULT '', outcome TEXT NOT NULL DEFAULT '', spec BLOB, result BLOB, error TEXT NOT NULL DEFAULT '', sweep_id TEXT NOT NULL DEFAULT '', metadata BLOB, created_at TEXT NOT NULL, started_at TEXT, finished_at TEXT)`,
		`CREATE TABLE events (seq INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL, session_id TEXT NOT NULL DEFAULT '', source TEXT NOT NULL, type TEXT NOT NULL, time TEXT NOT NULL, data BLOB, raw BLOB, schema_version INTEGER NOT NULL DEFAULT 1)`,
		`CREATE TABLE logs (seq INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL, session_id TEXT NOT NULL DEFAULT '', stream TEXT NOT NULL, time TEXT NOT NULL, text TEXT NOT NULL)`,
		`INSERT INTO events(run_id,source,type,time,data) VALUES('legacy','test','run.created','2026-01-01T00:00:00Z','{}')`,
		`INSERT INTO logs(run_id,stream,time,text) VALUES('legacy','stdout','2026-01-01T00:00:00Z','kept')`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	_ = raw.Close()
	if store, err := Open(path); err == nil {
		_ = store.Close()
		t.Fatal("legacy database was migrated in place")
	} else if !strings.Contains(err.Error(), "backup") {
		t.Fatalf("err = %v, want backup-then-rebuild guidance", err)
	}

	raw, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var version int
	if err := raw.QueryRow(`SELECT version FROM schema_meta`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 5 {
		t.Fatalf("version = %d, want legacy version 5", version)
	}
	var events, logs int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`SELECT COUNT(*) FROM logs`).Scan(&logs); err != nil {
		t.Fatal(err)
	}
	if events != 1 || logs != 1 {
		t.Fatalf("legacy rows changed: events=%d logs=%d", events, logs)
	}
}

func TestOpenInitializesAndReopensCurrentDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := store.db.QueryRow(`SELECT version FROM schema_meta`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("version = %d, want %d", version, schemaVersion)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.db.QueryRow(`SELECT version FROM schema_meta`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("reopened version = %d, want %d", version, schemaVersion)
	}
}

func TestCommitRoundTripsAndAppendsIntentVersions(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.CreateRun(ctx, harness.RunRecord{ID: "run-1", Phase: harness.RunPhaseRunning, Status: harness.StatusRunning, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	intent := harness.Intent{ID: "intent-1", RunID: "run-1", Kind: harness.IntentExplore, FromFactIDs: []string{"origin"}, Description: "probe", State: harness.IntentOpen, CreatedAt: now}
	event, err := harness.NewEvent("run-1", harness.EventIntentCreated, harness.IntentPayload{Intent: intent})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(ctx, harness.Commit{RunID: "run-1", Revision: 1, Intents: []harness.Intent{intent}, Events: []harness.Event{event}}); err != nil {
		t.Fatal(err)
	}
	intent.State = harness.IntentClaimed
	intent.Slot = 1
	intent.ExecutorID = "executor-1"
	if _, err := store.Commit(ctx, harness.Commit{RunID: "run-1", Revision: 2, Intents: []harness.Intent{intent}}); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListIntents(ctx, "run-1")
	if err != nil || len(items) != 1 || items[0].State != harness.IntentClaimed {
		t.Fatalf("intents = %+v err=%v", items, err)
	}
	var versions int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM intent_versions WHERE run_id='run-1' AND intent_id='intent-1'`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 2 {
		t.Fatalf("versions = %d, want 2", versions)
	}
	var commits int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM board_commits WHERE run_id='run-1'`).Scan(&commits); err != nil {
		t.Fatal(err)
	}
	if commits != 2 {
		t.Fatalf("commits = %d, want 2", commits)
	}
}

func TestArtifactContentAddressing(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	artifact, err := store.Put(context.Background(), harness.ArtifactInput{RunID: "run", AttemptID: "attempt", SessionID: "session", Name: "proof.txt", MediaType: "text/plain", Size: 5, Content: bytes.NewReader([]byte("proof"))})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := store.Open(context.Background(), artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	if _, err := data.ReadFrom(reader); err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	if data.String() != "proof" {
		t.Fatalf("data = %q", data.String())
	}
	if _, err := store.Open(context.Background(), "../bad"); err == nil {
		t.Fatal("unsafe artifact id accepted")
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = raw.Exec(`CREATE TABLE schema_meta(version INTEGER NOT NULL)`)
	_, _ = raw.Exec(`INSERT INTO schema_meta(version) VALUES(99)`)
	_ = raw.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("newer schema accepted")
	}
}

func TestFinalizeIsAtomicAndTerminalWritesAreRejected(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.CreateRun(ctx, harness.RunRecord{ID: "run-1", Phase: harness.RunPhaseRunning, Status: harness.StatusRunning, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	event, err := harness.NewEvent("run-1", harness.EventRunFinalized, harness.RunFinalizedPayload{Outcome: harness.RunOutcomeSucceeded, Status: harness.StatusSucceeded})
	if err != nil {
		t.Fatal(err)
	}
	verdict := &harness.VerdictRecord{RunID: "run-1", Success: true, Message: "ok", CreatedAt: now}
	record := harness.RunRecord{ID: "run-1", Phase: harness.RunPhaseTerminal, Outcome: harness.RunOutcomeSucceeded, Status: harness.StatusSucceeded, Result: json.RawMessage(`{"status":"succeeded"}`), CreatedAt: now, FinishedAt: &now}
	if _, err := store.Finalize(ctx, harness.FinalizationRecord{Run: record, Verdict: verdict, Event: event}); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetVerdict(ctx, "run-1")
	if err != nil || !stored.Success {
		t.Fatalf("verdict = %+v err=%v", stored, err)
	}
	late, _ := harness.NewEvent("run-1", harness.EventAgent, harness.AgentEventPayload{NativeType: "late"})
	if _, err := store.AppendEvent(ctx, late); err == nil {
		t.Fatal("event accepted after finalization")
	}
	if _, err := store.Commit(ctx, harness.Commit{RunID: "run-1", Revision: 2, Facts: []harness.Fact{{ID: "late", RunID: "run-1", TaskID: "task", Kind: harness.FactNegative, Description: "late"}}}); err == nil {
		t.Fatal("commit accepted after finalization")
	}
}

func TestMarkInterruptedFinalizesAtomically(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.CreateRun(ctx, harness.RunRecord{ID: "run-1", Phase: harness.RunPhaseRunning, Status: harness.StatusRunning, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	record, err := store.GetRun(ctx, "run-1")
	if err != nil || record.Phase != harness.RunPhaseTerminal || record.Outcome != harness.RunOutcomeInterrupted {
		t.Fatalf("record = %+v err=%v", record, err)
	}
	events, err := store.ListEvents(ctx, "run-1", 0)
	if err != nil || len(events) != 1 || events[0].Type != harness.EventRunFinalized {
		t.Fatalf("events = %+v err=%v", events, err)
	}
}
