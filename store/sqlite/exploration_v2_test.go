package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/red-copilot/harness/exploration"
)

func TestOpenMigratesV1WithoutDeletingAudit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE schema_meta (version INTEGER NOT NULL)`,
		`INSERT INTO schema_meta(version) VALUES(1)`,
		`CREATE TABLE runs (id TEXT PRIMARY KEY, status TEXT NOT NULL, spec BLOB, result BLOB, error TEXT NOT NULL DEFAULT '', sweep_id TEXT NOT NULL DEFAULT '', metadata BLOB, created_at TEXT NOT NULL, started_at TEXT, finished_at TEXT)`,
		`CREATE TABLE events (seq INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL, session_id TEXT NOT NULL DEFAULT '', source TEXT NOT NULL, type TEXT NOT NULL, time TEXT NOT NULL, data BLOB, raw BLOB)`,
		`CREATE TABLE logs (seq INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL, session_id TEXT NOT NULL DEFAULT '', stream TEXT NOT NULL, time TEXT NOT NULL, text TEXT NOT NULL)`,
		`INSERT INTO runs(id,status,created_at) VALUES('explore','running','2026-01-01T00:00:00Z')`,
		`INSERT INTO runs(id,status,created_at) VALUES('plain','running','2026-01-01T00:00:00Z')`,
		`INSERT INTO events(run_id,source,type,time) VALUES('explore','control','exploration.branch.created','2026-01-01T00:00:00Z')`,
		`INSERT INTO events(run_id,source,type,time) VALUES('plain','harness','run.started','2026-01-01T00:00:00Z')`,
		`INSERT INTO logs(run_id,stream,time,text) VALUES('explore','stdout','2026-01-01T00:00:00Z','old')`,
		`INSERT INTO logs(run_id,stream,time,text) VALUES('plain','stdout','2026-01-01T00:00:00Z','keep')`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	if got := countRows(t, store.db, "events"); got != 2 {
		t.Fatalf("events = %d, want both historical rows", got)
	}
	if got := countRows(t, store.db, "logs"); got != 2 {
		t.Fatalf("logs = %d, want both historical rows", got)
	}
	if got := countRows(t, store.db, "exploration_branches"); got != 0 {
		t.Fatalf("exploration_branches = %d, want empty", got)
	}
	var version int
	if err := store.db.QueryRow("SELECT version FROM schema_meta LIMIT 1").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("schema version = %d, want 2", version)
	}
	if matches, _ := filepath.Glob(path + "*bak*"); len(matches) != 0 {
		t.Fatalf("unexpected backup files: %v", matches)
	}
}

func TestKnowledgeStorePersistsTaskScopedKnowledge(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	item := exploration.Knowledge{
		ID: "k-1", TaskID: "task-1", Topic: "web.oa", Kind: exploration.KnowledgeAnchor,
		Statement: "OA path exists", Scope: exploration.KnowledgeScopeTarget, Target: "b-02",
		Trust: exploration.KnowledgeObserved, Reuse: exploration.ReuseStable,
		EvidenceRefs: []string{"obs-1"}, Sources: []string{"executor-1"},
		ValidFrom: time.Now().UTC(),
	}
	if err := store.SaveKnowledge(ctx, item); err != nil {
		t.Fatalf("SaveKnowledge: %v", err)
	}
	items, err := store.ListKnowledge(ctx, "task-1")
	if err != nil {
		t.Fatalf("ListKnowledge: %v", err)
	}
	if len(items) != 1 || items[0].ID != "k-1" {
		t.Fatalf("items = %+v", items)
	}
	item.Trust = exploration.KnowledgeCorroborated
	if err := store.UpdateKnowledge(ctx, item); err != nil {
		t.Fatalf("UpdateKnowledge: %v", err)
	}
	items, _ = store.ListKnowledge(ctx, "task-1")
	if items[0].Trust != exploration.KnowledgeCorroborated {
		t.Fatalf("trust = %q", items[0].Trust)
	}
	other, _ := store.ListKnowledge(ctx, "task-2")
	if len(other) != 0 {
		t.Fatalf("task-2 leaked task-1 knowledge: %+v", other)
	}
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestExplorationStorePersistsBranchesAttemptsAndObservations(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	branch := exploration.Branch{ID: "branch-1", TaskID: "task-1", RunID: "run-1", Depth: 1, Topic: "web.oa", Goal: "find OA", Hypothesis: "OA exists", Scope: "b-02", Status: exploration.BranchActive}
	if err := store.SaveBranch(ctx, branch); err != nil {
		t.Fatalf("SaveBranch: %v", err)
	}
	attempt := exploration.Attempt{ID: "attempt-1", BranchID: branch.ID, RunID: "run-1", SessionID: "executor-1", Status: exploration.AttemptCompleted, Outcome: exploration.AttemptLocalSuccess}
	if err := store.SaveAttempt(ctx, attempt); err != nil {
		t.Fatalf("SaveAttempt: %v", err)
	}
	observation := exploration.Observation{ID: "obs-1", BranchID: branch.ID, AttemptID: attempt.ID, Kind: "http", Summary: "HTTP 200", EvidenceRefs: []string{"raw-1"}}
	if err := store.SaveObservation(ctx, observation); err != nil {
		t.Fatalf("SaveObservation: %v", err)
	}
	if got := countRows(t, store.db, "exploration_branches"); got != 1 {
		t.Fatalf("branches = %d", got)
	}
	branches, err := store.ListBranches(ctx, "run-1")
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(branches) != 1 || branches[0].ID != branch.ID {
		t.Fatalf("branches = %+v", branches)
	}
	if got := countRows(t, store.db, "exploration_attempts"); got != 1 {
		t.Fatalf("attempts = %d", got)
	}
	if got := countRows(t, store.db, "exploration_observations"); got != 1 {
		t.Fatalf("observations = %d", got)
	}
}
