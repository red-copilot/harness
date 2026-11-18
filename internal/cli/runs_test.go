package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/store/sqlite"
)

// seedRun writes one non-terminal run plus its board records directly through
// the canonical store, so the read-only commands have real data to expose.
func seedRun(t *testing.T, dbPath string) string {
	t.Helper()
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	runID := "run-seed-1"
	spec, _ := json.Marshal(map[string]any{
		"task":   map[string]any{"id": "task-1", "objective": "probe"},
		"target": map[string]any{"id": "target-1"},
	})
	if err := store.CreateRun(ctx, harness.RunRecord{ID: runID, Phase: harness.RunPhaseRunning, Spec: spec, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(ctx, harness.Commit{
		RunID: runID, ExpectedRevision: 0, Revision: 1,
		Claims: []harness.Claim{{ID: "origin", RunID: runID, TaskID: "task-1", Kind: harness.ClaimOrigin, Description: "probe", CreatedAt: now}},
		Intents: []harness.Intent{{
			ID: "intent-1", RunID: runID, FromClaimIDs: []string{"origin"}, State: harness.IntentOpen,
			Description: "probe the target", CreatedAt: now,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	return runID
}

func runCLI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	var err error
	if code := Main(args, &stdout, &stderr); code != 0 {
		err = errors.New(strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), stderr.String(), err
}

func TestRunsListShowsSeededRun(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "harness.db")
	runID := seedRun(t, dbPath)
	stdout, _, err := runCLI(t, "runs", "list", "--db", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, runID) || !strings.Contains(stdout, "running") {
		t.Fatalf("list output = %q, want run id and phase", stdout)
	}
	stdout, _, err = runCLI(t, "runs", "list", "--db", dbPath, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var summaries []runSummary
	if err := json.Unmarshal([]byte(stdout), &summaries); err != nil {
		t.Fatalf("decode list json: %v (%q)", err, stdout)
	}
	if len(summaries) != 1 || summaries[0].RunID != runID || summaries[0].TaskID != "task-1" || summaries[0].TargetID != "target-1" {
		t.Fatalf("summaries = %+v", summaries)
	}
}

func TestRunsShowSummarizesBoardCounts(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "harness.db")
	runID := seedRun(t, dbPath)
	stdout, _, err := runCLI(t, "runs", "show", "--db", dbPath, "--id", runID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "claims=1") || !strings.Contains(stdout, "intents=1") {
		t.Fatalf("show output = %q, want board counts", stdout)
	}
	// The raw Spec/Result blobs must not leak into the summary.
	if strings.Contains(stdout, `"objective"`) {
		t.Fatalf("show output leaked raw spec: %q", stdout)
	}
}

func TestRunsBoardAndArtifactsExposeRecords(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "harness.db")
	runID := seedRun(t, dbPath)
	stdout, _, err := runCLI(t, "runs", "board", "--db", dbPath, "--id", runID)
	if err != nil {
		t.Fatal(err)
	}
	var board struct {
		RunID   string           `json:"run_id"`
		Claims  []harness.Claim  `json:"claims"`
		Intents []harness.Intent `json:"intents"`
	}
	if err := json.Unmarshal([]byte(stdout), &board); err != nil {
		t.Fatalf("decode board json: %v (%q)", err, stdout)
	}
	if board.RunID != runID || len(board.Claims) != 1 || len(board.Intents) != 1 {
		t.Fatalf("board = %+v", board)
	}
	stdout, _, err = runCLI(t, "runs", "artifacts", "--db", dbPath, "--id", runID)
	if err != nil {
		t.Fatal(err)
	}
	var artifacts []harness.Artifact
	if err := json.Unmarshal([]byte(stdout), &artifacts); err != nil {
		t.Fatalf("decode artifacts json: %v (%q)", err, stdout)
	}
	if len(artifacts) != 0 {
		t.Fatalf("artifacts = %+v, want none seeded", artifacts)
	}
}

func TestRunsLogsTail(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "harness.db")
	runID := seedRun(t, dbPath)
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := store.AppendLog(context.Background(), harness.LogRecord{
			RunID: runID, SessionID: "executor", Stream: "stdout",
			Time: time.Now().UTC(), Text: "line",
		}); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()
	stdout, _, err := runCLI(t, "runs", "logs", "--db", dbPath, "--id", runID, "--tail", "2")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(strings.TrimSpace(stdout), "\n") + 1; got != 2 {
		t.Fatalf("tail printed %d lines, want 2:\n%s", got, stdout)
	}
}

func TestRunsCancelRefusesWhileLockHeld(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "harness.db")
	runID := seedRun(t, dbPath)
	lock, err := recoveryGuard(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, stderr, cancelErr := runCLI(t, "runs", "cancel", "--db", dbPath, "--id", runID)
	if cancelErr == nil {
		t.Fatal("cancel succeeded while another process owns the database")
	}
	if !strings.Contains(cancelErr.Error(), "live owner") && !strings.Contains(strings.ToLower(stderr), "live owner") {
		t.Fatalf("err = %v, want live-owner refusal", cancelErr)
	}
	_ = lock()
}

func TestRunsCancelFinalizesOrphanRun(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "harness.db")
	runID := seedRun(t, dbPath)
	stdout, _, err := runCLI(t, "runs", "cancel", "--db", dbPath, "--id", runID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "cancelled") {
		t.Fatalf("output = %q, want cancellation confirmation", stdout)
	}
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	record, err := store.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Phase != harness.RunPhaseTerminal || record.Outcome != harness.RunOutcomeInterrupted {
		t.Fatalf("record = %s/%s, want terminal/interrupted", record.Phase, record.Outcome)
	}
	// A finalized run cannot be cancelled again.
	if _, _, err := runCLI(t, "runs", "cancel", "--db", dbPath, "--id", runID); err == nil {
		t.Fatal("terminal run cancelled twice")
	}
}

func TestRunsUnknownSubcommand(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "harness.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runCLI(t, "runs", "bogus", "--db", dbPath); err == nil {
		t.Fatal("unknown runs subcommand accepted")
	}
}
