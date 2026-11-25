// Package porttest is the shared conformance suite for the v5 persistence
// ports. It runs unchanged against the native v5 journal store and the v4
// compatibility bridge, so both must implement identical semantics.
package porttest

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/red-copilot/harness/core"
	"github.com/red-copilot/harness/engine"
)

// Factory opens one isolated store for a subtest.
type Factory func(t *testing.T) engine.V5Store

type item struct {
	typ     core.RecordType
	payload any
}

type fixture struct {
	store    engine.V5Store
	redactor *core.Redactor
	runID    string
	manifest core.Manifest
	digest   string
	state    core.State
}

// Run executes the conformance suite. Each subtest opens its own store.
func Run(t *testing.T, newStore Factory) {
	t.Helper()
	t.Run("Lifecycle", func(t *testing.T) { testLifecycle(t, newStore(t)) })
	t.Run("StaleHeadAndAtomicity", func(t *testing.T) { testStaleHead(t, newStore(t)) })
	t.Run("TerminalGuards", func(t *testing.T) { testTerminalGuards(t, newStore(t)) })
	t.Run("ResumeNormalization", func(t *testing.T) { testResume(t, newStore(t)) })
	t.Run("RetryReopensIntent", func(t *testing.T) { testRetryReopensIntent(t, newStore(t)) })
	t.Run("ArtifactAndLogs", func(t *testing.T) { testArtifactAndLogs(t, newStore(t)) })
}

func newFixture(t *testing.T, store engine.V5Store) *fixture {
	t.Helper()
	ctx := context.Background()
	at := time.Unix(1_700_000_000, 0).UTC()
	manifest := core.Manifest{
		SchemaVersion: core.ManifestSchemaVersion,
		RunID:         "run-1",
		Task:          core.Task{ID: "task-1", Objective: "conformance run"},
		Target:        core.TargetSpec{ID: "target-1", Addresses: []string{"10.0.0.1:80"}, Isolation: core.IsolationCallerManaged},
		Policy:        core.DefaultRunPolicy(),
		Reasoner:      core.AgentDescriptor{ID: "reasoner"},
		Executor:      core.AgentDescriptor{ID: "executor"},
		Verifier:      core.VerifierDescriptor{ID: "verifier"},
		CreatedAt:     at,
	}
	redactor := core.NewRedactor()
	sealedManifest, err := redactor.SealManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := sealedManifest.Manifest().Digest()
	if err != nil {
		t.Fatal(err)
	}
	records := records(t, manifest.RunID, 1, digest, at, item{core.RecordRunCreated, core.RunCreatedRecord{
		TaskID: "task-1", TargetID: "target-1", ManifestDigest: digest, ReasonerID: "reasoner", ExecutorID: "executor",
	}})
	state, err := store.Create(ctx, sealedManifest, sealBatch(t, redactor, manifest.RunID, 0, &core.State{RunID: manifest.RunID, Phase: core.PhaseQueued}, 0, records))
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != core.PhaseQueued || state.HeadSeq != 1 {
		t.Fatalf("created state = %+v", state)
	}
	return &fixture{store: store, redactor: redactor, runID: manifest.RunID, manifest: manifest, digest: digest, state: state}
}

func records(t *testing.T, runID string, start uint64, prev string, at time.Time, items ...item) []core.Record {
	t.Helper()
	out := make([]core.Record, 0, len(items))
	for i, it := range items {
		record, err := core.NewRecord(runID, start+uint64(i), it.typ, at, it.payload, prev)
		if err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
		out = append(out, record)
		prev = record.Hash
	}
	return out
}

func sealBatch(t *testing.T, redactor *core.Redactor, runID string, expected uint64, state *core.State, revision uint64, recs []core.Record) core.SealedBatch {
	t.Helper()
	sealed, err := redactor.SealBatch(core.Batch{RunID: runID, ExpectedHead: expected, State: state, BoardRevision: revision, Records: recs})
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func (f *fixture) append(t *testing.T, phase core.Phase, revision uint64, at time.Time, mutate func(*core.State), items ...item) core.State {
	t.Helper()
	next := f.state
	next.HeadSeq, next.HeadHash = 0, ""
	next.Phase = phase
	next.BoardRevision = revision
	if mutate != nil {
		mutate(&next)
	}
	recs := records(t, f.runID, f.state.HeadSeq+1, f.state.HeadHash, at, items...)
	result, err := f.store.Append(context.Background(), sealBatch(t, f.redactor, f.runID, f.state.HeadSeq, &next, revision, recs))
	if err != nil {
		t.Fatalf("append %s: %v", phase, err)
	}
	f.state = result.State
	return result.State
}

func (f *fixture) finalize(t *testing.T, outcome core.Outcome, verdict *core.Verdict, at time.Time) core.State {
	t.Helper()
	recs := records(t, f.runID, f.state.HeadSeq+1, f.state.HeadHash, at, item{core.RecordRunFinalized, core.RunFinalizedRecord{Outcome: outcome, At: at}})
	sealed, err := f.redactor.SealFinalization(core.Finalization{
		RunID: f.runID, ExpectedHead: f.state.HeadSeq, Outcome: outcome,
		Verdict: verdict, Records: recs, FinishedAt: at,
	})
	if err != nil {
		t.Fatalf("seal finalization: %v", err)
	}
	result, err := f.store.Finalize(context.Background(), sealed)
	if err != nil {
		t.Fatalf("finalize %s: %v", outcome, err)
	}
	f.state = result.State
	return result.State
}

func (f *fixture) originClaim(at time.Time) item {
	return item{core.RecordClaimCreated, core.ClaimCreatedRecord{Claim: core.Claim{
		ID: "origin", RunID: f.runID, TaskID: "task-1", Kind: core.ClaimOrigin, Description: "objective", CreatedAt: at,
	}}}
}

func (f *fixture) goalClaim(at time.Time) item {
	return item{core.RecordClaimCreated, core.ClaimCreatedRecord{Claim: core.Claim{
		ID: "goal", RunID: f.runID, TaskID: "task-1", Kind: core.ClaimGoal, Description: "goal", CreatedAt: at,
	}}}
}

func (f *fixture) phaseRecord(from, to core.Phase, at time.Time) item {
	return item{core.RecordRunPhase, core.RunPhaseRecord{From: from, To: to, At: at}}
}

func testLifecycle(t *testing.T, store engine.V5Store) {
	ctx := context.Background()
	f := newFixture(t, store)
	if nonTerminal, err := store.NonTerminal(ctx); err != nil || len(nonTerminal) != 1 {
		t.Fatalf("non-terminal = %d, err = %v", len(nonTerminal), err)
	}
	at := time.Unix(1_700_000_001, 0).UTC()
	f.append(t, core.PhaseProvisioning, 1, at, nil, f.phaseRecord(core.PhaseQueued, core.PhaseProvisioning, at), f.originClaim(at))
	at = at.Add(time.Second)
	f.append(t, core.PhaseRunning, 2, at, nil, f.phaseRecord(core.PhaseProvisioning, core.PhaseRunning, at), f.goalClaim(at))
	at = at.Add(time.Second)
	f.append(t, core.PhaseVerifying, 2, at, func(s *core.State) { s.GoalCandidate = true },
		item{core.RecordRunGoalCandidate, core.RunGoalCandidateRecord{At: at}},
		f.phaseRecord(core.PhaseRunning, core.PhaseVerifying, at))
	if f.state.Phase != core.PhaseVerifying || !f.state.GoalCandidate {
		t.Fatalf("state before finalize = %+v", f.state)
	}
	if err := store.VerifyChain(ctx, f.runID); err != nil {
		t.Fatalf("chain before finalize: %v", err)
	}
	finalAt := at.Add(time.Second)
	final := f.finalize(t, core.OutcomeSucceeded, &core.Verdict{Authority: core.VerdictAuthorityVerifier, Success: true}, finalAt)
	if final.Phase != core.PhaseTerminal || final.Outcome != core.OutcomeSucceeded {
		t.Fatalf("final state = %+v", final)
	}
	if err := store.VerifyChain(ctx, f.runID); err != nil {
		t.Fatalf("chain after finalize: %v", err)
	}
	board, err := store.Board(ctx, f.runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(board.Claims) != 2 || board.Revision != 2 {
		t.Fatalf("board = %+v", board)
	}
	verdict, err := store.Verdict(ctx, f.runID)
	if err != nil || verdict == nil || !verdict.Success {
		t.Fatalf("verdict = %+v, err = %v", verdict, err)
	}
	restored, err := store.Replay(ctx, f.runID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.State.Outcome != core.OutcomeSucceeded || len(restored.Board.Claims) != 2 || restored.Board.Revision != 2 {
		t.Fatalf("restored = %+v", restored)
	}
	manifest, err := store.Manifest(ctx, f.runID)
	if err != nil || manifest.RunID != f.runID || manifest.SchemaVersion != core.ManifestSchemaVersion {
		t.Fatalf("manifest = %+v, err = %v", manifest, err)
	}
	if nonTerminal, err := store.NonTerminal(ctx); err != nil || len(nonTerminal) != 0 {
		t.Fatalf("non-terminal after finalize = %d, err = %v", len(nonTerminal), err)
	}
	events, err := store.Events(ctx, f.runID, 0)
	if err != nil || len(events) == 0 {
		t.Fatalf("events = %d, err = %v", len(events), err)
	}
	if events[0].Type != core.RecordRunCreated || events[len(events)-1].Type != core.RecordRunFinalized {
		t.Fatalf("unexpected event bounds: %s .. %s", events[0].Type, events[len(events)-1].Type)
	}
}

func testStaleHead(t *testing.T, store engine.V5Store) {
	f := newFixture(t, store)
	ctx := context.Background()
	at := time.Unix(1_700_000_010, 0).UTC()
	// A internally consistent batch that does not extend the persisted head.
	recs := records(t, f.runID, 1, f.digest, at, item{core.RecordRunState, core.RunStateRecord{}})
	_, err := store.Append(ctx, sealBatch(t, f.redactor, f.runID, 0, nil, 0, recs))
	if !errors.Is(err, core.ErrStaleHead) {
		t.Fatalf("stale expected head err = %v", err)
	}
	// Correct expected head, wrong previous hash.
	badPrev := strings.Repeat("b", 64)
	recs = records(t, f.runID, f.state.HeadSeq+1, badPrev, at, item{core.RecordRunState, core.RunStateRecord{}})
	_, err = store.Append(ctx, sealBatch(t, f.redactor, f.runID, f.state.HeadSeq, nil, 0, recs))
	if !errors.Is(err, core.ErrStaleHead) {
		t.Fatalf("bad previous hash err = %v", err)
	}
	state, err := store.State(ctx, f.runID)
	if err != nil {
		t.Fatal(err)
	}
	if state.HeadSeq != 1 || state.Phase != core.PhaseQueued {
		t.Fatalf("rejected append mutated state: %+v", state)
	}
	events, err := store.Events(ctx, f.runID, 0)
	if err != nil || len(events) != 1 {
		t.Fatalf("rejected append persisted records: %d, err = %v", len(events), err)
	}
}

func testTerminalGuards(t *testing.T, store engine.V5Store) {
	f := newFixture(t, store)
	ctx := context.Background()
	at := time.Unix(1_700_000_020, 0).UTC()
	// A successful verdict cannot finalize a Run that never reached verifying.
	if _, err := store.Finalize(ctx, mustSealFinalization(t, f, core.OutcomeSucceeded, &core.Verdict{Authority: core.VerdictAuthorityVerifier, Success: true}, at)); err == nil {
		t.Fatal("success finalized from queued phase")
	}
	f.finalize(t, core.OutcomeFailed, nil, at)
	if _, err := store.Append(ctx, sealBatch(t, f.redactor, f.runID, f.state.HeadSeq, &core.State{RunID: f.runID, Phase: core.PhaseRunning}, 0, records(t, f.runID, f.state.HeadSeq+1, f.state.HeadHash, at, item{core.RecordRunState, core.RunStateRecord{}}))); !errors.Is(err, core.ErrTerminal) {
		t.Fatalf("append after terminal err = %v", err)
	}
	if _, err := store.Finalize(ctx, mustSealFinalization(t, f, core.OutcomeFailed, nil, at)); !errors.Is(err, core.ErrTerminal) {
		t.Fatalf("second finalize err = %v", err)
	}
}

func mustSealFinalization(t *testing.T, f *fixture, outcome core.Outcome, verdict *core.Verdict, at time.Time) core.SealedFinalization {
	t.Helper()
	recs := records(t, f.runID, f.state.HeadSeq+1, f.state.HeadHash, at, item{core.RecordRunFinalized, core.RunFinalizedRecord{Outcome: outcome, At: at}})
	sealed, err := f.redactor.SealFinalization(core.Finalization{
		RunID: f.runID, ExpectedHead: f.state.HeadSeq, Outcome: outcome,
		Verdict: verdict, Records: recs, FinishedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func testResume(t *testing.T, store engine.V5Store) {
	ctx := context.Background()
	f := newFixture(t, store)
	at := time.Unix(1_700_000_030, 0).UTC()
	intent := core.Intent{ID: "intent-1", RunID: f.runID, FromClaimIDs: []string{"origin"}, Description: "probe", State: core.IntentOpen, CreatedAt: at}
	attempt := core.IntentAttempt{ID: "attempt-1", RunID: f.runID, IntentID: intent.ID, SessionID: "executor", Phase: core.IntentAttemptRunning, StartedAt: at}
	f.append(t, core.PhaseProvisioning, 1, at, nil, f.originClaim(at))
	f.append(t, core.PhaseRunning, 2, at, nil, item{core.RecordIntentProposed, core.IntentProposedRecord{Intent: intent}})
	f.append(t, core.PhaseRunning, 4, at, nil, item{core.RecordIntentClaimed, core.IntentClaimedRecord{IntentID: intent.ID, ExecutorID: "executor", At: at}})
	f.append(t, core.PhaseRunning, 4, at, nil, item{core.RecordAttemptStarted, core.AttemptStartedRecord{Attempt: attempt}})
	f.append(t, core.PhaseRunning, 5, at, nil, item{core.RecordRunResumed, core.RunResumedRecord{HeadSeq: f.state.HeadSeq - 1, Normalizations: core.ResumeNormalizations}})

	board, err := store.Board(ctx, f.runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(board.Intents) != 1 || board.Intents[0].State != core.IntentOpen || board.Intents[0].ExecutorID != "" {
		t.Fatalf("resumed intents = %+v", board.Intents)
	}
	if len(board.Attempts) != 1 || board.Attempts[0].Phase != core.IntentAttemptInterrupted {
		t.Fatalf("resumed attempts = %+v", board.Attempts)
	}
	restored, err := store.Replay(ctx, f.runID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Board.Intents[0].State != core.IntentOpen || restored.Board.Attempts[0].Phase != core.IntentAttemptInterrupted {
		t.Fatalf("replayed board = %+v", restored.Board)
	}
	if restored.Board.Revision != 5 {
		t.Fatalf("replayed revision = %d, want 5", restored.Board.Revision)
	}
}

func testArtifactAndLogs(t *testing.T, store engine.V5Store) {
	ctx := context.Background()
	f := newFixture(t, store)
	at := time.Unix(1_700_000_040, 0).UTC()
	sealed, err := f.redactor.SealArtifact(core.ArtifactInput{
		RunID: f.runID, AttemptID: "attempt-1", SessionID: "executor", Name: "proof.txt",
		MediaType: "text/plain", Content: strings.NewReader("proof"),
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := store.Put(ctx, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.ID != artifact.SHA256 || len(artifact.SHA256) != 64 {
		t.Fatalf("artifact identity = %+v", artifact)
	}
	f.append(t, core.PhaseProvisioning, 2, at, nil,
		f.originClaim(at),
		item{core.RecordArtifactStored, core.ArtifactStoredRecord{Artifact: artifact}},
	)
	artifacts, err := store.ListArtifacts(ctx, f.runID)
	if err != nil || len(artifacts) != 1 {
		t.Fatalf("artifacts = %+v, err = %v", artifacts, err)
	}
	reader, err := store.OpenArtifact(ctx, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if closeErr := reader.Close(); err == nil {
		err = closeErr
	}
	if err != nil || string(data) != "proof" {
		t.Fatalf("artifact content = %q, err = %v", data, err)
	}
	secret := "supersecret"
	logRedactor := core.NewRedactor(secret)
	log := logRedactor.Redact("token=" + secret)
	if strings.Contains(log, secret) {
		t.Fatal("redactor leaked the secret")
	}
	if err := store.AppendLogs(ctx, f.runID, []core.SealedLog{logRedactor.SealLog(core.LogRecord{
		RunID: f.runID, Stream: "stdout", Time: at, Text: "token=" + secret,
	})}); err != nil {
		t.Fatal(err)
	}
	logs, err := store.Logs(ctx, f.runID, 0)
	if err != nil || len(logs) != 1 || strings.Contains(logs[0].Text, secret) {
		t.Fatalf("logs = %+v, err = %v", logs, err)
	}
}

func testRetryReopensIntent(t *testing.T, store engine.V5Store) {
	ctx := context.Background()
	f := newFixture(t, store)
	at := time.Unix(1_700_000_035, 0).UTC()
	intent := core.Intent{ID: "intent-1", RunID: f.runID, FromClaimIDs: []string{"origin"}, Description: "probe", State: core.IntentOpen, CreatedAt: at}
	f.append(t, core.PhaseProvisioning, 1, at, nil, f.originClaim(at))
	f.append(t, core.PhaseRunning, 3, at, nil, f.goalClaim(at), item{core.RecordIntentProposed, core.IntentProposedRecord{Intent: intent}})
	f.append(t, core.PhaseRunning, 4, at, nil, item{core.RecordIntentClaimed, core.IntentClaimedRecord{IntentID: intent.ID, ExecutorID: "executor", At: at}})
	f.append(t, core.PhaseRunning, 5, at, nil, item{core.RecordIntentReopened, core.IntentReopenedRecord{IntentID: intent.ID, At: at}})
	board, err := store.Board(ctx, f.runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(board.Intents) != 1 || board.Intents[0].State != core.IntentOpen || board.Intents[0].ExecutorID != "" {
		t.Fatalf("reopened intents = %+v", board.Intents)
	}
	if board.Revision != 5 {
		t.Fatalf("revision = %d, want 5", board.Revision)
	}
	restored, err := store.Replay(ctx, f.runID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Board.Intents[0].State != core.IntentOpen {
		t.Fatalf("replayed intent = %+v", restored.Board.Intents[0])
	}
}
