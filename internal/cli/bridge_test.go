package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/red-copilot/harness/core"
	"github.com/red-copilot/harness/store/sqlite"
)

func seedBridgeRun(t *testing.T, path string) string {
	t.Helper()
	store, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bridge := sqlite.NewV4Bridge(store)
	ctx := context.Background()
	at := time.Unix(1_700_000_900, 0).UTC()
	manifest := core.Manifest{
		SchemaVersion: core.ManifestSchemaVersion, RunID: "run-1",
		Task:     core.Task{ID: "task-1", Objective: "bridge cli"},
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
	return "run-1"
}

func TestCLIReadsAndCancelsABridgeRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.db")
	runID := seedBridgeRun(t, path)
	var out, errOut bytes.Buffer
	if err := runsShow([]string{"--db", path, "--id", runID}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"run-1", "task-1", "target-1", "claims=1"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("runs show output missing %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if err := runsCancel([]string{"--db", path, "--id", runID}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "cancelled") {
		t.Fatalf("cancel output = %q", out.String())
	}
	store, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bridge := sqlite.NewV4Bridge(store)
	state, err := bridge.State(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != core.PhaseTerminal || state.Outcome != core.OutcomeInterrupted {
		t.Fatalf("state after cancel = %+v", state)
	}
	events, err := bridge.Events(context.Background(), runID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if events[len(events)-1].Type != core.RecordRunInterrupted {
		t.Fatalf("last record = %s, want run.interrupted", events[len(events)-1].Type)
	}
	if err := bridge.VerifyChain(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
}
