package harness_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/red-copilot/harness"
)

type claimedIntentCommitFailureStore struct {
	harness.Store
	err error
}

func (s *claimedIntentCommitFailureStore) Commit(ctx context.Context, commit harness.Commit) ([]harness.Event, error) {
	for _, intent := range commit.Intents {
		if intent.State == harness.IntentClaimed {
			return nil, s.err
		}
	}
	return s.Store.Commit(ctx, commit)
}

func TestCommitFailureDoesNotLeakUncommittedBoardIntoResult(t *testing.T) {
	inner := newMemoryStore()
	spec := integrationSpec(t, inner)
	spec.Verifier = fakeVerifier{verdict: harness.Verdict{Success: false}}
	spec.ReasonStrategy = oneIntentReasonStrategy{}
	spec.Policy.Exploration.MaxAttemptsPerIntent = 1
	spec.Policy.Exploration.StagnationRounds = 1
	spec.Reasoner.Runtime = &fakeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		return promptReportSession(req.WorkspaceDir, `{"board_revision":1,"intents":[{"from":["origin"],"description":"probe"}]}`)
	}}
	spec.Executor.Runtime = &fakeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		return promptReportSession(req.WorkspaceDir, `{"phase":"failed","error":"not attempted"}`)
	}}
	commitErr := errors.New("canonical commit failed")
	store := &claimedIntentCommitFailureStore{Store: inner, err: commitErr}

	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "canonical commit failed") {
		t.Fatalf("err = %v, want commit failure", err)
	}
	if result.Outcome != harness.RunOutcomeFailed {
		t.Fatalf("outcome = %q, want failed", result.Outcome)
	}
	if len(result.Intents) != 0 {
		t.Fatalf("result leaked uncommitted intents: %+v", result.Intents)
	}
	intents, err := inner.ListIntents(context.Background(), result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 0 {
		t.Fatalf("store contains uncommitted intents: %+v", intents)
	}
}
