package harness_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/core"
	"github.com/red-copilot/harness/store/sqlite"
)

// sqliteResumeEnv builds a real SQLite store plus a spec whose reasoner proposes
// one intent and whose executor settles it with an evidence-backed negative.
func sqliteResumeEnv(t *testing.T) (*sqlite.Store, harness.RunSpec) {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	spec := integrationSpec(t, store)
	spec.ReasonStrategy = oneIntentReasonStrategy{}
	spec.Policy.Exploration.MaxTotalIntents = 4
	spec.Policy.Exploration.StagnationRounds = 2
	return store, spec
}

// TestResumeRestoresBoardAndContinues seeds the database with a crashed
// process's leftover state — a run stuck in the running phase with a committed
// board revision — then resumes and asserts the run continues from the
// replayed board to a terminal outcome without duplicating board history.
func TestResumeRestoresBoardAndContinues(t *testing.T) {
	store, working := sqliteResumeEnv(t)
	ctx := context.Background()
	runID := "crashed-run-1"
	now := time.Now().UTC()
	// A crashed process leaves: a run record still in running phase and the
	// board revisions it committed before dying.
	if err := store.CreateRun(ctx, harness.RunRecord{ID: runID, Phase: harness.RunPhaseRunning, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(ctx, harness.Commit{
		RunID: runID, ExpectedRevision: 0, Revision: 1,
		Claims: []core.Claim{
			{ID: "origin", RunID: runID, TaskID: working.Task.ID, Kind: core.ClaimOrigin, Description: working.Task.Objective, CreatedAt: now},
			{ID: "goal", RunID: runID, TaskID: working.Task.ID, Kind: core.ClaimGoal, Description: working.Task.Objective, CreatedAt: now},
		},
		Intents: []core.Intent{{ID: "intent-1", RunID: runID, FromClaimIDs: []string{"origin"}, State: core.IntentClaimed, ExecutorID: "executor", Description: "probe the target", CreatedAt: now}},
	}); err != nil {
		t.Fatal(err)
	}

	resumeRunner := newTestRunner(t, store)
	resumed, err := resumeRunner.Resume(ctx, runID, working)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	result, waitErr := resumed.Wait(ctx)
	if result.Phase != harness.RunPhaseTerminal {
		t.Fatalf("resumed run phase = %q, want terminal", result.Phase)
	}
	if waitErr != nil {
		t.Fatalf("resumed run error: %v", waitErr)
	}
	// The resumed board kept the replayed history: no duplicate origin/goal
	// claims, and the claimed intent returned to open before continuing.
	claims, err := store.ListClaims(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, claim := range claims {
		kinds[string(claim.Kind)]++
	}
	if kinds["origin"] != 1 || kinds["goal"] != 1 {
		t.Fatalf("claims = %+v, want exactly one origin and one goal", kinds)
	}
	intents, err := store.ListIntents(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) == 0 {
		t.Fatal("resumed board lost its intents")
	}
	// No intent may stay claimed after a resume: the slot starts free.
	for _, intent := range intents {
		if intent.State == harness.IntentClaimed {
			t.Fatalf("intent %s stayed claimed after resume", intent.ID)
		}
	}
}

// TestResumeRejectsTerminalRun pins the fail-closed contract: a finished run
// cannot be resumed.
func TestResumeRejectsTerminalRun(t *testing.T) {
	store, spec := sqliteResumeEnv(t)
	runner := newTestRunner(t, store)
	result, err := runner.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Phase != harness.RunPhaseTerminal {
		t.Fatalf("phase = %q, want terminal", result.Phase)
	}
	resumeRunner := newTestRunner(t, store)
	if _, err := resumeRunner.Resume(context.Background(), result.RunID, spec); err == nil {
		t.Fatal("terminal run accepted for resume")
	}
}
