package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/core"
)

func commitFixture(now time.Time) harness.Commit {
	return harness.Commit{
		RunID: "run-1", ExpectedRevision: 0, Revision: 1,
		Claims: []core.Claim{{ID: "claim-1", RunID: "run-1", TaskID: "task", Kind: core.ClaimOrigin, Description: "origin", CreatedAt: now}},
		Intents: []core.Intent{{
			ID: "intent-1", RunID: "run-1", State: core.IntentOpen, Description: "probe",
			CreatedAt: now,
		}},
	}
}

func TestRestoreBoardStateReplaysLatestCommit(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.CreateRun(ctx, harness.RunRecord{ID: "run-1", Phase: harness.RunPhaseRunning, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(ctx, commitFixture(now)); err != nil {
		t.Fatal(err)
	}
	commit, err := store.RestoreBoardState(ctx, "run-1")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if commit.RunID != "run-1" || commit.Revision != 1 {
		t.Fatalf("commit = %s rev %d, want run-1 rev 1", commit.RunID, commit.Revision)
	}
	if len(commit.Intents) != 1 || commit.Intents[0].ID != "intent-1" {
		t.Fatalf("intents = %+v, want replayed intent-1", commit.Intents)
	}
}

func TestRestoreBoardStateFailsClosed(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.CreateRun(ctx, harness.RunRecord{ID: "run-1", Phase: harness.RunPhaseRunning, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RestoreBoardState(ctx, "run-1"); err == nil {
		t.Fatal("restore without any commit accepted")
	}

	if _, err := store.Commit(ctx, commitFixture(now)); err != nil {
		t.Fatal(err)
	}
	// Corrupt the newest snapshot payload; restore must fail closed.
	if _, err := store.db.Exec(`UPDATE board_commits SET payload='{"run_id":"run-1","revision":1,"claims":' WHERE run_id=? AND revision=1`, "run-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RestoreBoardState(ctx, "run-1"); err == nil {
		t.Fatal("damaged board commit accepted")
	}
}

func TestRestoreBoardStateRejectsRevisionMismatch(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.CreateRun(ctx, harness.RunRecord{ID: "run-1", Phase: harness.RunPhaseRunning, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(ctx, commitFixture(now)); err != nil {
		t.Fatal(err)
	}
	// Sneak a commit row whose revision column disagrees with its payload.
	payload := `{"run_id":"run-1","revision":1,"claims":[],"intents":[],"attempts":[],"observations":[],"artifacts":[],"events":[]}`
	if _, err := store.db.Exec(`INSERT INTO board_commits(run_id,revision,payload,created_at) VALUES(?,?,?,?)`, "run-1", 2, payload, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RestoreBoardState(ctx, "run-1"); err == nil {
		t.Fatal("revision-mismatched commit accepted")
	}
}

func TestLatestBoardCommitRejectsForeignRunID(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.CreateRun(ctx, harness.RunRecord{ID: "run-1", Phase: harness.RunPhaseRunning, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(ctx, commitFixture(now)); err != nil {
		t.Fatal(err)
	}
	payload := `{"run_id":"run-2","revision":1,"claims":[],"intents":[],"attempts":[],"observations":[],"artifacts":[],"events":[]}`
	if _, err := store.db.Exec(`UPDATE board_commits SET payload=? WHERE run_id=?`, payload, "run-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LatestBoardCommit(ctx, "run-1"); err == nil {
		t.Fatal("foreign run_id in commit payload accepted")
	}
}
