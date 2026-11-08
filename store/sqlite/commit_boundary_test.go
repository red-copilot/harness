package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

func TestCommitRejectsCrossRunRecords(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	for _, id := range []string{"run-1", "run-2"} {
		if err := store.CreateRun(ctx, harness.RunRecord{ID: id, Phase: harness.RunPhaseRunning, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	_, err = store.Commit(ctx, harness.Commit{
		RunID: "run-1", Revision: 1,
		Claims: []harness.Claim{{ID: "claim-1", RunID: "run-2", TaskID: "task", Kind: harness.ClaimFinding, Description: "cross-run", ObservationIDs: []string{"obs"}, CreatedAt: now}},
	})
	if err == nil {
		t.Fatal("cross-run claim accepted")
	}
}

func TestRunningAttemptFinishedAtStaysEmpty(t *testing.T) {
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
	intent := harness.Intent{ID: "intent-1", RunID: "run-1", FromClaimIDs: []string{"origin"}, Description: "probe", State: harness.IntentOpen, CreatedAt: now}
	attempt := harness.IntentAttempt{ID: "attempt-1", RunID: "run-1", IntentID: intent.ID, SessionID: "executor", Phase: harness.IntentAttemptRunning, StartedAt: now}
	if _, err := store.Commit(ctx, harness.Commit{RunID: "run-1", Revision: 1, Intents: []harness.Intent{intent}, Attempts: []harness.IntentAttempt{attempt}}); err != nil {
		t.Fatal(err)
	}
	var finished string
	if err := store.db.QueryRow(`SELECT finished_at FROM attempts WHERE run_id='run-1' AND id='attempt-1'`).Scan(&finished); err != nil {
		t.Fatal(err)
	}
	if finished != "" {
		t.Fatalf("running attempt finished_at = %q, want empty", finished)
	}
}
