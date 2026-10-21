package sqlite_test

import (
	"context"
	"database/sql"
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
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM knowledge_versions WHERE run_id='run' AND knowledge_id='k1'`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 2 {
		t.Fatalf("versions = %d, want 2", versions)
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
