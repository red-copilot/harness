package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/store/sqlite"
)

func TestMigrationPreservesAuditRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`CREATE TABLE schema_meta(version INTEGER NOT NULL)`,
		`INSERT INTO schema_meta(version) VALUES(1)`,
		`CREATE TABLE runs(id TEXT PRIMARY KEY,status TEXT NOT NULL,spec BLOB,result BLOB,error TEXT NOT NULL DEFAULT '',sweep_id TEXT NOT NULL DEFAULT '',metadata BLOB,created_at TEXT NOT NULL,started_at TEXT,finished_at TEXT)`,
		`INSERT INTO runs(id,status,created_at) VALUES('old','failed','2026-01-01T00:00:00Z')`,
		`CREATE TABLE events(seq INTEGER PRIMARY KEY AUTOINCREMENT,run_id TEXT NOT NULL,session_id TEXT NOT NULL DEFAULT '',source TEXT NOT NULL,type TEXT NOT NULL,time TEXT NOT NULL,data BLOB,raw BLOB)`,
		`INSERT INTO events(run_id,source,type,time,data) VALUES('old','legacy','legacy.event','2026-01-01T00:00:00Z','{"keep":true}')`,
		`CREATE TABLE logs(seq INTEGER PRIMARY KEY AUTOINCREMENT,run_id TEXT NOT NULL,session_id TEXT NOT NULL DEFAULT '',stream TEXT NOT NULL,time TEXT NOT NULL,text TEXT NOT NULL)`,
		`INSERT INTO logs(run_id,stream,time,text) VALUES('old','stdout','2026-01-01T00:00:00Z','preserve me')`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	_ = db.Close()
	store, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	events, err := store.ListEvents(context.Background(), "old", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || !strings.Contains(string(events[0].Data), "keep") {
		t.Fatalf("events = %+v", events)
	}
	logs, err := store.ListLogs(context.Background(), "old", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].Text != "preserve me" {
		t.Fatalf("logs = %+v", logs)
	}
}

func TestMigrationIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.db")
	for i := 0; i < 2; i++ {
		store, err := sqlite.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		_ = store.Close()
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow(`SELECT version FROM schema_meta LIMIT 1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 3 {
		t.Fatalf("version = %d", version)
	}
}

func TestRunScopedIDsAreUniqueAcrossRuns(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	for _, runID := range []string{"run-a", "run-b"} {
		if err := store.SaveBranch(ctx, harness.Branch{ID: "branch-1", RunID: runID, TaskID: "task", Kind: harness.BranchRoot, Topic: "root", Scope: "target", Status: harness.BranchOpen, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveAttempt(ctx, harness.Attempt{ID: "attempt-1", RunID: runID, BranchID: "branch-1", SessionID: "s", Status: harness.AttemptRunning, StartedAt: now, FinishedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := store.ListBranches(ctx, "run-a")
	b, _ := store.ListBranches(ctx, "run-b")
	if len(a) != 1 || len(b) != 1 || a[0].RunID != "run-a" || b[0].RunID != "run-b" {
		t.Fatalf("branches a=%+v b=%+v", a, b)
	}
}

func TestKnowledgeVersionsAreAppendOnly(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	item := harness.Knowledge{ID: "k1", TaskID: "task", RunID: "run", Topic: "topic", Statement: "first", Trust: harness.KnowledgeObserved, Scope: harness.KnowledgeScopeTask, Reuse: harness.ReuseStable, ValidFrom: now, Version: 1}
	if err := store.SaveKnowledge(ctx, item); err != nil {
		t.Fatal(err)
	}
	item.Statement = "second"
	item.Trust = harness.KnowledgeCorroborated
	item.Version = 2
	if err := store.UpdateKnowledge(ctx, item); err != nil {
		t.Fatal(err)
	}
	items, err := store.QueryKnowledge(ctx, harness.KnowledgeQuery{TaskID: "task", RunID: "run", AllowCrossRun: true, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Statement != "second" {
		t.Fatalf("latest = %+v", items)
	}
	var versions int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM knowledge WHERE run_id='run' AND id='k1'`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 2 {
		t.Fatalf("versions = %d, want 2", versions)
	}
	// A published version is immutable: rewriting it must fail rather than
	// silently replace audit history.
	item.Version = 1
	item.Statement = "rewritten"
	if err := store.UpdateKnowledge(ctx, item); err == nil {
		t.Fatal("rewriting a published knowledge version was accepted")
	}
}

func TestQueryKnowledgeFiltersAtStoreBoundary(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	expired := now.Add(-time.Minute)
	items := []harness.Knowledge{
		{ID: "stable", TaskID: "task", RunID: "old", Target: "target", Scope: harness.KnowledgeScopeTask, Reuse: harness.ReuseStable, Trust: harness.KnowledgeObserved, ValidFrom: now, Version: 1},
		{ID: "never", TaskID: "task", RunID: "old", Target: "target", Scope: harness.KnowledgeScopeTask, Reuse: harness.ReuseNever, Trust: harness.KnowledgeObserved, ValidFrom: now, Version: 1},
		{ID: "expired", TaskID: "task", RunID: "old", Target: "target", Scope: harness.KnowledgeScopeInstance, Reuse: harness.ReuseInstance, Trust: harness.KnowledgeObserved, ValidFrom: now, ExpiresAt: &expired, Version: 1},
		{ID: "other-task", TaskID: "other", RunID: "old", Target: "target", Scope: harness.KnowledgeScopeTask, Reuse: harness.ReuseStable, Trust: harness.KnowledgeObserved, ValidFrom: now, Version: 1},
	}
	for _, item := range items {
		if err := store.SaveKnowledge(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.QueryKnowledge(ctx, harness.KnowledgeQuery{TaskID: "task", RunID: "new", TargetFingerprint: "target", AllowCrossRun: true, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "stable" {
		t.Fatalf("query = %+v", got)
	}
}

func TestAppendArtifactMetadataAndBytes(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	artifact, err := store.Put(ctx, harness.ArtifactInput{RunID: "run", AttemptID: "attempt", SessionID: "session", Name: "proof.txt", MediaType: "text/plain", Size: int64(len("proof")), Content: strings.NewReader("proof")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveArtifact(ctx, artifact); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListArtifacts(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].SHA256 != artifact.ID {
		t.Fatalf("metadata = %+v", items)
	}
	reader, err := store.Open(ctx, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(reader)
	_ = reader.Close()
	if string(data) != "proof" {
		t.Fatalf("bytes = %q", data)
	}
}

func TestArtifactOpenRejectsUnknownAndTraversalID(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, id := range []string{"../secret", "aaaa/bbbb", strings.Repeat("a", 64)} {
		if _, err := store.Open(context.Background(), id); err == nil {
			t.Fatalf("artifact id %q accepted", id)
		}
	}
	_ = os.Remove(filepath.Join(t.TempDir(), "unused"))
}

// TestOpenRejectsNewerSchemaVersion: a database written by a newer build must
// not be silently downgraded and reinterpreted by an older one.
func TestOpenRejectsNewerSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.db")
	store, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`UPDATE schema_meta SET version=99`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.Open(path); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("Open err = %v, want newer-schema rejection", err)
	}
}

// TestFinalizeCommitsTerminalStateTogether: the terminal Run record, its
// verdict and run.finalized are one canonical write, so an audit reader can
// never observe one without the others.
func TestFinalizeCommitsTerminalStateTogether(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := store.CreateRun(ctx, harness.RunRecord{ID: "run-1", Status: harness.StatusRunning, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	event, err := store.Finalize(ctx, harness.FinalizationRecord{
		Run:     harness.RunRecord{ID: "run-1", Status: harness.StatusSucceeded, Result: []byte(`{"status":"succeeded"}`), FinishedAt: &now},
		Verdict: &harness.VerdictRecord{RunID: "run-1", Success: true, Score: 0.9, Message: "verified", CreatedAt: now},
		Event:   harness.Event{RunID: "run-1", Source: "harness", Type: "run.finalized", Time: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.Seq == 0 {
		t.Fatal("finalize did not assign an event sequence")
	}
	record, err := store.GetRun(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != harness.StatusSucceeded || record.FinishedAt == nil || string(record.Result) != `{"status":"succeeded"}` {
		t.Fatalf("run record = %+v", record)
	}
	verdict, err := store.GetVerdict(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.Success || verdict.Message != "verified" {
		t.Fatalf("verdict = %+v", verdict)
	}
	events, err := store.ListEvents(ctx, "run-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "run.finalized" {
		t.Fatalf("events = %+v", events)
	}
}

// TestFinalizeRollsBackOnMissingRun: a failed terminal write must not leave
// the verdict behind as a partial audit record.
func TestFinalizeRollsBackOnMissingRun(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	_, err = store.Finalize(ctx, harness.FinalizationRecord{
		Run:     harness.RunRecord{ID: "missing", Status: harness.StatusSucceeded, FinishedAt: &now},
		Verdict: &harness.VerdictRecord{RunID: "missing", Success: true, Message: "verified", CreatedAt: now},
		Event:   harness.Event{RunID: "missing", Source: "harness", Type: "run.finalized", Time: now},
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Finalize err = %v, want not found", err)
	}
	if _, err := store.GetVerdict(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("partial verdict survived rollback: %v", err)
	}
	events, err := store.ListEvents(ctx, "missing", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("partial events survived rollback: %+v", events)
	}
}
