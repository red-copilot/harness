package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/red-copilot/harness/core"
	"github.com/red-copilot/harness/internal/lease"
)

const v5TestDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type v5RecordItem struct {
	typ     core.RecordType
	payload any
}

func v5Manifest(runID string) core.Manifest {
	return core.Manifest{
		SchemaVersion: core.ManifestSchemaVersion,
		RunID:         runID,
		Task:          core.Task{ID: "task-1", Objective: "test the target"},
		Target:        core.TargetSpec{ID: "target-1", Addresses: []string{"10.0.0.1:80"}, Isolation: core.IsolationCallerManaged},
		Policy:        core.DefaultRunPolicy(),
		Reasoner:      core.AgentDescriptor{ID: "reasoner"},
		Executor:      core.AgentDescriptor{ID: "executor"},
		Verifier:      core.VerifierDescriptor{ID: "verifier"},
		CreatedAt:     time.Unix(1, 0).UTC(),
	}
}

func v5SealManifest(t *testing.T, runID string) (core.SealedManifest, core.Manifest, string) {
	t.Helper()
	manifest := v5Manifest(runID)
	sealed, err := core.NewRedactor().SealManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := manifest.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return sealed, manifest, digest
}

func v5Records(t *testing.T, runID string, start uint64, prev string, at time.Time, items ...v5RecordItem) []core.Record {
	t.Helper()
	records := make([]core.Record, 0, len(items))
	for i, item := range items {
		record, err := core.NewRecord(runID, start+uint64(i), item.typ, at, item.payload, prev)
		if err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
		records = append(records, record)
		prev = record.Hash
	}
	return records
}

func v5SealBatch(t *testing.T, runID string, head uint64, state *core.State, revision uint64, records []core.Record) core.SealedBatch {
	t.Helper()
	sealed, err := core.NewRedactor().SealBatch(core.Batch{RunID: runID, ExpectedHead: head, State: state, BoardRevision: revision, Records: records})
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func v5SealFinalization(t *testing.T, runID string, head uint64, outcome core.Outcome, verdict *core.Verdict, records []core.Record, at time.Time) core.SealedFinalization {
	t.Helper()
	sealed, err := core.NewRedactor().SealFinalization(core.Finalization{
		RunID: runID, ExpectedHead: head, Outcome: outcome, Verdict: verdict,
		Records: records, FinishedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func v5CreateRun(t *testing.T, store *V5Store, runID string) (core.State, string) {
	t.Helper()
	sealedManifest, _, digest := v5SealManifest(t, runID)
	at := time.Unix(2, 0).UTC()
	records := v5Records(t, runID, 1, digest, at, v5RecordItem{core.RecordRunCreated, core.RunCreatedRecord{
		TaskID: "task-1", TargetID: "target-1", ManifestDigest: digest, ReasonerID: "reasoner", ExecutorID: "executor",
	}})
	state, err := store.Create(context.Background(), sealedManifest, v5SealBatch(t, runID, 0, &core.State{RunID: runID, Phase: core.PhaseQueued}, 0, records))
	if err != nil {
		t.Fatal(err)
	}
	return state, digest
}

func v5Open(t *testing.T) (*V5Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "harness.db")
	store, err := OpenV5Fresh(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}

func TestV5CreateAppendFinalizeAndReplay(t *testing.T) {
	store, _ := v5Open(t)
	ctx := context.Background()
	state, _ := v5CreateRun(t, store, "run-1")
	if state.Phase != core.PhaseQueued || state.HeadSeq != 1 {
		t.Fatalf("state = %+v", state)
	}
	at := time.Unix(3, 0).UTC()
	origin := core.Claim{ID: "origin", RunID: "run-1", TaskID: "task-1", Kind: core.ClaimOrigin, Description: "goal", CreatedAt: at}
	records := v5Records(t, "run-1", state.HeadSeq+1, state.HeadHash, at,
		v5RecordItem{core.RecordRunPhase, core.RunPhaseRecord{From: core.PhaseQueued, To: core.PhaseProvisioning, At: at}},
		v5RecordItem{core.RecordClaimCreated, core.ClaimCreatedRecord{Claim: origin}},
	)
	result, err := store.Append(ctx, v5SealBatch(t, "run-1", state.HeadSeq, &core.State{RunID: "run-1", Phase: core.PhaseProvisioning}, 1, records))
	if err != nil {
		t.Fatal(err)
	}
	state = result.State
	if state.Phase != core.PhaseProvisioning || state.BoardRevision != 1 || state.HeadSeq != 3 {
		t.Fatalf("state = %+v", state)
	}
	goal := core.Claim{ID: "goal", RunID: "run-1", TaskID: "task-1", Kind: core.ClaimGoal, Description: "goal", CreatedAt: at}
	records = v5Records(t, "run-1", state.HeadSeq+1, state.HeadHash, at,
		v5RecordItem{core.RecordRunPhase, core.RunPhaseRecord{From: core.PhaseProvisioning, To: core.PhaseRunning, At: at}},
		v5RecordItem{core.RecordClaimCreated, core.ClaimCreatedRecord{Claim: goal}},
	)
	result, err = store.Append(ctx, v5SealBatch(t, "run-1", state.HeadSeq, &core.State{RunID: "run-1", Phase: core.PhaseRunning}, 2, records))
	if err != nil {
		t.Fatal(err)
	}
	state = result.State
	records = v5Records(t, "run-1", state.HeadSeq+1, state.HeadHash, at,
		v5RecordItem{core.RecordRunGoalCandidate, core.RunGoalCandidateRecord{At: at}},
		v5RecordItem{core.RecordRunPhase, core.RunPhaseRecord{From: core.PhaseRunning, To: core.PhaseVerifying, At: at}},
	)
	verifyingState := core.State{RunID: "run-1", Phase: core.PhaseVerifying, GoalCandidate: true, BoardRevision: 2}
	result, err = store.Append(ctx, v5SealBatch(t, "run-1", state.HeadSeq, &verifyingState, 2, records))
	if err != nil {
		t.Fatal(err)
	}
	state = result.State
	finalAt := time.Unix(4, 0).UTC()
	finalRecords := v5Records(t, "run-1", state.HeadSeq+1, state.HeadHash, finalAt,
		v5RecordItem{core.RecordRunFinalized, core.RunFinalizedRecord{Outcome: core.OutcomeSucceeded, At: finalAt}},
	)
	finalized, err := store.Finalize(ctx, v5SealFinalization(t, "run-1", state.HeadSeq, core.OutcomeSucceeded, &core.Verdict{Authority: core.VerdictAuthorityVerifier, Success: true}, finalRecords, finalAt))
	if err != nil {
		t.Fatal(err)
	}
	if finalized.State.Phase != core.PhaseTerminal || finalized.State.Outcome != core.OutcomeSucceeded {
		t.Fatalf("state = %+v", finalized.State)
	}
	if err := store.VerifyChain(ctx, "run-1"); err != nil {
		t.Fatal(err)
	}
	board, err := store.Board(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(board.Claims) != 2 || board.Revision != 2 {
		t.Fatalf("board = %+v", board)
	}
	verdict, err := store.Verdict(ctx, "run-1")
	if err != nil || verdict == nil || !verdict.Success {
		t.Fatalf("verdict = %+v, err = %v", verdict, err)
	}
	restored, err := store.Replay(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Board.Claims) != 2 || restored.Board.Revision != 2 || restored.State.Outcome != core.OutcomeSucceeded {
		t.Fatalf("restored = %+v", restored)
	}
	if err := store.RebuildReadModel(ctx, "run-1"); err != nil {
		t.Fatal(err)
	}
	board, err = store.Board(ctx, "run-1")
	if err != nil || len(board.Claims) != 2 {
		t.Fatalf("rebuilt board = %+v, err = %v", board, err)
	}
}

func TestV5StaleHeadAndTerminalGuards(t *testing.T) {
	store, _ := v5Open(t)
	ctx := context.Background()
	state, _ := v5CreateRun(t, store, "run-1")
	at := time.Unix(3, 0).UTC()
	stalePrev := strings.Repeat("b", 64)
	records := v5Records(t, "run-1", 3, stalePrev, at, v5RecordItem{core.RecordRunState, core.RunStateRecord{}})
	if _, err := store.Append(ctx, v5SealBatch(t, "run-1", 2, nil, 0, records)); !errors.Is(err, core.ErrStaleHead) {
		t.Fatalf("stale head err = %v", err)
	}
	finalRecords := v5Records(t, "run-1", state.HeadSeq+1, state.HeadHash, at, v5RecordItem{core.RecordRunFinalized, core.RunFinalizedRecord{Outcome: core.OutcomeFailed, At: at}})
	nonTerminal, err := store.NonTerminal(ctx)
	if err != nil || len(nonTerminal) != 1 {
		t.Fatalf("non-terminal before finalize = %d, err = %v", len(nonTerminal), err)
	}
	if _, err := store.Finalize(ctx, v5SealFinalization(t, "run-1", state.HeadSeq, core.OutcomeFailed, nil, finalRecords, at)); err != nil {
		t.Fatal(err)
	}
	nonTerminal, err = store.NonTerminal(ctx)
	if err != nil || len(nonTerminal) != 0 {
		t.Fatalf("non-terminal after finalize = %d, err = %v", len(nonTerminal), err)
	}
	if _, err := store.Append(ctx, v5SealBatch(t, "run-1", 2, nil, 0, records)); !errors.Is(err, core.ErrTerminal) {
		t.Fatalf("append after terminal err = %v", err)
	}
	if _, err := store.Finalize(ctx, v5SealFinalization(t, "run-1", state.HeadSeq, core.OutcomeFailed, nil, finalRecords, at)); !errors.Is(err, core.ErrTerminal) {
		t.Fatalf("second finalize err = %v", err)
	}
}

func TestV5FinalizationRequiresVerdictAtSeal(t *testing.T) {
	at := time.Unix(3, 0).UTC()
	final, err := core.NewRecord("run-1", 2, core.RecordRunFinalized, at, core.RunFinalizedRecord{Outcome: core.OutcomeSucceeded, At: at}, v5TestDigest)
	if err != nil {
		t.Fatal(err)
	}
	_, err = core.NewRedactor().SealFinalization(core.Finalization{
		RunID: "run-1", ExpectedHead: 1, Outcome: core.OutcomeSucceeded,
		Records: []core.Record{final}, FinishedAt: at,
	})
	if err == nil {
		t.Fatal("success without verdict sealed")
	}
}

func TestV5Interrupt(t *testing.T) {
	store, _ := v5Open(t)
	ctx := context.Background()
	state, _ := v5CreateRun(t, store, "run-1")
	at := time.Unix(3, 0).UTC()
	records := v5Records(t, "run-1", state.HeadSeq+1, state.HeadHash, at, v5RecordItem{core.RecordRunInterrupted, core.RunInterruptedRecord{Reason: "operator", At: at}})
	sealed, err := core.NewRedactor().SealFinalization(core.Finalization{
		RunID: "run-1", ExpectedHead: state.HeadSeq, Outcome: core.OutcomeInterrupted,
		Error: "operator", Records: records, FinishedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	next, err := store.Interrupt(ctx, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if next.Phase != core.PhaseTerminal || next.Outcome != core.OutcomeInterrupted {
		t.Fatalf("state = %+v", next)
	}
}

func TestV5FaultInjectionRollsBack(t *testing.T) {
	store, _ := v5Open(t)
	ctx := context.Background()
	state, _ := v5CreateRun(t, store, "run-1")
	at := time.Unix(3, 0).UTC()
	records := v5Records(t, "run-1", state.HeadSeq+1, state.HeadHash, at, v5RecordItem{core.RecordClaimCreated, core.ClaimCreatedRecord{Claim: core.Claim{ID: "origin", RunID: "run-1", TaskID: "task-1", Kind: core.ClaimOrigin, Description: "goal", CreatedAt: at}}})
	store.testAfterJournal = func() error { return errors.New("injected") }
	if _, err := store.Append(ctx, v5SealBatch(t, "run-1", state.HeadSeq, &core.State{RunID: "run-1", Phase: core.PhaseProvisioning}, 1, records)); err == nil {
		t.Fatal("injected append succeeded")
	}
	store.testAfterJournal = nil
	current, err := store.State(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if current.HeadSeq != state.HeadSeq || current.Phase != core.PhaseQueued {
		t.Fatalf("partial append persisted: %+v", current)
	}
	events, err := store.Events(ctx, "run-1", 0)
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %d, err = %v", len(events), err)
	}
}

func TestV5ArtifactsAndLogs(t *testing.T) {
	store, _ := v5Open(t)
	ctx := context.Background()
	state, _ := v5CreateRun(t, store, "run-1")
	at := time.Unix(3, 0).UTC()
	sealed, err := core.NewRedactor().SealArtifact(core.ArtifactInput{
		RunID: "run-1", AttemptID: "attempt-1", SessionID: "executor", Name: "proof.txt",
		MediaType: "text/plain", Content: strings.NewReader("proof"),
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := store.Put(ctx, sealed)
	if err != nil {
		t.Fatal(err)
	}
	records := v5Records(t, "run-1", state.HeadSeq+1, state.HeadHash, at, v5RecordItem{core.RecordArtifactStored, core.ArtifactStoredRecord{Artifact: artifact}})
	result, err := store.Append(ctx, v5SealBatch(t, "run-1", state.HeadSeq, &core.State{RunID: "run-1", Phase: core.PhaseProvisioning}, 1, records))
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := store.ListArtifacts(ctx, "run-1")
	if err != nil || len(artifacts) != 1 {
		t.Fatalf("artifacts = %+v, err = %v", artifacts, err)
	}
	reader, err := store.OpenArtifact(ctx, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 5)
	if _, err := reader.Read(data); err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	if string(data) != "proof" {
		t.Fatalf("artifact = %q", data)
	}
	secret := "supersecret"
	logRecord := core.NewRedactor(secret).SealLog(core.LogRecord{RunID: "run-1", Stream: "stdout", Time: at, Text: "token=" + secret})
	if err := store.AppendLogs(ctx, "run-1", []core.SealedLog{logRecord}); err != nil {
		t.Fatal(err)
	}
	logs, err := store.Logs(ctx, "run-1", 0)
	if err != nil || len(logs) != 1 || strings.Contains(logs[0].Text, secret) {
		t.Fatalf("logs = %+v, err = %v", logs, err)
	}
	finalRecords := v5Records(t, "run-1", result.State.HeadSeq+1, result.State.HeadHash, at, v5RecordItem{core.RecordRunFinalized, core.RunFinalizedRecord{Outcome: core.OutcomeFailed, At: at}})
	if _, err := store.Finalize(ctx, v5SealFinalization(t, "run-1", result.State.HeadSeq, core.OutcomeFailed, nil, finalRecords, at)); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendLogs(ctx, "run-1", []core.SealedLog{logRecord}); !errors.Is(err, core.ErrTerminal) {
		t.Fatalf("log after terminal err = %v", err)
	}
}

func TestV5LeaseAndVersionRejection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.db")
	store, err := OpenV5Fresh(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenV5Writer(path); !errors.Is(err, lease.ErrHeld) {
		t.Fatalf("second writer err = %v", err)
	}
	reader, err := OpenV5Reader(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenV5Fresh(path); err == nil {
		t.Fatal("OpenV5Fresh overwrote an existing database")
	}
	if _, err := OpenV5Writer(path); err != nil {
		t.Fatalf("reopen after release: %v", err)
	}

	v8Path := filepath.Join(t.TempDir(), "v8.db")
	v8, err := OpenFresh(v8Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := v8.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenV5Writer(v8Path); err == nil {
		t.Fatal("v5 writer accepted a v8 database")
	}
	if _, err := OpenV5Reader(v8Path); err == nil {
		t.Fatal("v5 reader accepted a v8 database")
	}
}

func TestV5CutoverFromVerifiedBackup(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.db")
	v8, err := OpenFresh(live)
	if err != nil {
		t.Fatal(err)
	}
	if err := v8.Close(); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "v8-backup.db")
	manifestPath := filepath.Join(t.TempDir(), "v8-backup.json")
	if _, err := CreateBackup(live, backup, manifestPath); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "archive-v8")
	store, err := CutoverV5(live, manifestPath, archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenV5Reader(live)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var version int
	if err := reader.db.QueryRow(`SELECT version FROM schema_meta WHERE id=1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 5 {
		t.Fatalf("version = %d", version)
	}
	if _, err := os.Stat(filepath.Join(archive, "live.db")); err != nil {
		t.Fatalf("archived v8 database missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(archive, "cutover.json")); err != nil {
		t.Fatalf("cutover record missing: %v", err)
	}
}

func TestV5VerifyChainDetectsTamper(t *testing.T) {
	store, _ := v5Open(t)
	ctx := context.Background()
	state, _ := v5CreateRun(t, store, "run-1")
	at := time.Unix(3, 0).UTC()
	records := v5Records(t, "run-1", state.HeadSeq+1, state.HeadHash, at, v5RecordItem{core.RecordClaimCreated, core.ClaimCreatedRecord{Claim: core.Claim{ID: "origin", RunID: "run-1", TaskID: "task-1", Kind: core.ClaimOrigin, Description: "goal", CreatedAt: at}}})
	if _, err := store.Append(ctx, v5SealBatch(t, "run-1", state.HeadSeq, &core.State{RunID: "run-1", Phase: core.PhaseProvisioning}, 1, records)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `DROP TRIGGER run_journal_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE run_journal SET payload=? WHERE run_id=? AND seq=?`, []byte(`{"claim":{"id":"tampered"}}`), "run-1", state.HeadSeq); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyChain(ctx, "run-1"); !errors.Is(err, core.ErrCorruptJournal) {
		t.Fatalf("tampered chain err = %v", err)
	}
}

func TestV5ReplayFallsBackFromCorruptCheckpoint(t *testing.T) {
	store, _ := v5Open(t)
	ctx := context.Background()
	state, _ := v5CreateRun(t, store, "run-1")
	at := time.Unix(3, 0).UTC()
	records := v5Records(t, "run-1", state.HeadSeq+1, state.HeadHash, at, v5RecordItem{core.RecordClaimCreated, core.ClaimCreatedRecord{Claim: core.Claim{ID: "origin", RunID: "run-1", TaskID: "task-1", Kind: core.ClaimOrigin, Description: "goal", CreatedAt: at}}})
	if _, err := store.Append(ctx, v5SealBatch(t, "run-1", state.HeadSeq, &core.State{RunID: "run-1", Phase: core.PhaseProvisioning}, 1, records)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE run_checkpoints SET state_sha256=? WHERE run_id=?`, strings.Repeat("0", 64), "run-1"); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Replay(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Board.Claims) != 1 || restored.Board.Revision != 1 {
		t.Fatalf("restored board = %+v", restored.Board)
	}
}

func TestV5OpenArtifactDetectsTamper(t *testing.T) {
	store, _ := v5Open(t)
	ctx := context.Background()
	sealed, err := core.NewRedactor().SealArtifact(core.ArtifactInput{RunID: "run-1", AttemptID: "attempt-1", Name: "proof.txt", Content: strings.NewReader("proof")})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := store.Put(ctx, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `DROP TRIGGER artifact_blobs_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE artifact_blobs SET data=? WHERE sha256=?`, []byte("other"), artifact.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OpenArtifact(ctx, artifact.ID); err == nil {
		t.Fatal("tampered artifact accepted")
	}
}
