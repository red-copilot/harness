package harness_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/red-copilot/harness"
)

func TestRedactingStoreCoversBranchesResultsAndVerdicts(t *testing.T) {
	secret := "super-secret-value"
	t.Setenv("TEST_SECRET", secret)
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.SecretEnv = []string{"TEST_SECRET"}
	spec.PlannerStrategy = staticPlannerStrategy{proposal: harness.PlanProposal{CreateBranches: []harness.BranchProposal{{ParentID: "root", Topic: "web." + secret, Goal: secret, Hypothesis: secret, Target: "target-1", SuccessCondition: secret, FailureCondition: secret}}}}
	spec.Verifier = fakeVerifier{verdict: harness.Verdict{Success: true, Message: "verified " + secret, Evidence: json.RawMessage(`{"note":"` + secret + `"}`)}}
	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	branches, _ := store.ListBranches(context.Background(), result.RunID)
	events, _ := store.ListEvents(context.Background(), result.RunID, 0)
	verdict, _ := store.GetVerdict(context.Background(), result.RunID)
	runRecord, _ := store.GetRun(context.Background(), result.RunID)
	payloads := []string{string(mustMarshal(branches)), string(mustMarshal(events)), string(mustMarshal(verdict)), string(mustMarshal(runRecord)), string(mustMarshal(result))}
	for _, payload := range payloads {
		if strings.Contains(payload, secret) {
			t.Fatalf("secret leaked: %s", payload)
		}
	}
}

func mustMarshal(value any) []byte { data, _ := json.Marshal(value); return data }
