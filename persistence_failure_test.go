package harness_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/red-copilot/harness"
)

func TestArtifactWriteFailureStopsRun(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.Artifacts = &fakeArtifactStore{fail: errors.New("artifact disk unavailable")}
	spec.ExecutorStrategy = staticExecutorStrategy{report: harness.AttemptReport{Status: harness.AttemptCompleted, Outcome: harness.AttemptLocalSuccess, Observations: []harness.ObservationProposal{{LocalID: "obs", Kind: "k", Summary: "s", Target: "target-1", EvidenceFiles: []string{"proof.txt"}}}}}
	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "artifact") {
		t.Fatalf("err = %v", err)
	}
	if result.Status != harness.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Verdict != nil && result.Verdict.Success {
		t.Fatalf("success verdict after artifact failure: %+v", result.Verdict)
	}
}

type failingBranchStore struct {
	*memoryStore
	fail bool
}

func (s *failingBranchStore) SaveBranch(ctx context.Context, branch harness.Branch) error {
	if s.fail {
		return errors.New("branch write unavailable")
	}
	return s.memoryStore.SaveBranch(ctx, branch)
}

func TestStoreWriteFailureStopsRun(t *testing.T) {
	base := newMemoryStore()
	store := &failingBranchStore{memoryStore: base, fail: true}
	spec := integrationSpec(t, base)
	runner, err := harness.NewRunner(harness.Config{Store: store, WorkspaceRoot: t.TempDir(), SkipRecovery: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "branch write") {
		t.Fatalf("err = %v", err)
	}
	if result.Status != harness.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
}
