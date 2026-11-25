package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/red-copilot/harness/core"
	"github.com/red-copilot/harness/engine"
	"github.com/red-copilot/harness/engine/porttest"
)

func TestV4BridgeConformance(t *testing.T) {
	porttest.Run(t, func(t *testing.T) engine.V5Store {
		t.Helper()
		store, err := Open(filepath.Join(t.TempDir(), "harness.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return NewV4Bridge(store)
	})
}

// TestV4BridgeKeepsV8ReadSurface proves the compatibility bridge leaves a
// normal v8 database behind: the schema version stays 8 and the existing read
// commands see the run, its events and its board.
func TestV4BridgeKeepsV8ReadSurface(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.db")
	base, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	bridge := NewV4Bridge(base)
	ctx := context.Background()
	at := time.Unix(1_700_000_100, 0).UTC()
	manifest := core.Manifest{
		SchemaVersion: core.ManifestSchemaVersion, RunID: "run-1",
		Task:     core.Task{ID: "task-1", Objective: "bridge"},
		Target:   core.TargetSpec{ID: "target-1", Isolation: core.IsolationCallerManaged},
		Policy:   core.DefaultRunPolicy(),
		Reasoner: core.AgentDescriptor{ID: "reasoner"}, Executor: core.AgentDescriptor{ID: "executor"},
		Verifier: core.VerifierDescriptor{ID: "verifier"}, CreatedAt: at,
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
	created, err := core.NewRecord("run-1", 1, core.RecordRunCreated, at, core.RunCreatedRecord{TaskID: "task-1", TargetID: "target-1", ManifestDigest: digest}, digest)
	if err != nil {
		t.Fatal(err)
	}
	first, err := redactor.SealBatch(core.Batch{RunID: "run-1", ExpectedHead: 0, State: &core.State{RunID: "run-1", Phase: core.PhaseQueued}, Records: []core.Record{created}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := bridge.Create(ctx, sealedManifest, first)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := core.NewRecord("run-1", state.HeadSeq+1, core.RecordClaimCreated, at, core.ClaimCreatedRecord{Claim: core.Claim{
		ID: "origin", RunID: "run-1", TaskID: "task-1", Kind: core.ClaimOrigin, Description: "objective", CreatedAt: at,
	}}, state.HeadHash)
	if err != nil {
		t.Fatal(err)
	}
	next := core.State{RunID: "run-1", Phase: core.PhaseProvisioning}
	batch, err := redactor.SealBatch(core.Batch{RunID: "run-1", ExpectedHead: state.HeadSeq, State: &next, BoardRevision: 1, Records: []core.Record{claim}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.Append(ctx, batch); err != nil {
		t.Fatal(err)
	}

	reader, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var version int
	if err := reader.db.QueryRow(`SELECT version FROM schema_meta LIMIT 1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 8 {
		t.Fatalf("schema version = %d, want 8", version)
	}
	run, err := reader.GetRun(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.Phase != core.RunPhaseProvisioning {
		t.Fatalf("run phase = %q", run.Phase)
	}
	events, err := reader.ListEvents(ctx, "run-1", 0)
	if err != nil || len(events) != 2 {
		t.Fatalf("events = %d, err = %v", len(events), err)
	}
	if events[0].Type != core.EventType(core.RecordRunCreated) || events[1].Type != core.EventType(core.RecordClaimCreated) {
		t.Fatalf("event types = %s, %s", events[0].Type, events[1].Type)
	}
	claims, err := reader.ListClaims(ctx, "run-1")
	if err != nil || len(claims) != 1 || claims[0].ID != "origin" {
		t.Fatalf("claims = %+v, err = %v", claims, err)
	}
	if revision, err := reader.GetBoardRevision(ctx, "run-1"); err != nil || revision != 1 {
		t.Fatalf("board revision = %d, err = %v", revision, err)
	}
}

// TestV4BridgeIgnoresLegacyRuns keeps the bridge from adopting v4 history.
func TestV4BridgeIgnoresLegacyRuns(t *testing.T) {
	base, err := Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	ctx := context.Background()
	if err := base.CreateRun(ctx, core.RunRecord{ID: "v4-run", Phase: core.RunPhaseRunning, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	bridge := NewV4Bridge(base)
	nonTerminal, err := bridge.NonTerminal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nonTerminal) != 0 {
		t.Fatalf("legacy run adopted by bridge: %+v", nonTerminal)
	}
	if _, err := bridge.State(ctx, "v4-run"); err == nil || errors.Is(err, core.ErrNotFound) {
		t.Fatalf("legacy run state err = %v, want not-a-bridge-run", err)
	}
}
