package harness_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

// TestReviewBranchResolvesConflictPair drives a full run in which one attempt
// promotes a claim, a second attempt refutes it, and the review branch opened
// for that pair adjudicates both sides.
func TestReviewBranchResolvesConflictPair(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.PlannerStrategy = conflictReviewPlanner{}
	spec.ExecutorStrategy = conflictReviewExecutor{}
	runner := newTestRunner(t, store)
	result, err := runner.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != harness.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", result.Status)
	}
	trust := map[string]harness.KnowledgeTrust{}
	for _, item := range result.Knowledge {
		trust[item.Statement] = item.Trust
	}
	if trust["service is exposed"] != harness.KnowledgeCorroborated {
		t.Fatalf("winner trust = %q, want corroborated (%+v)", trust["service is exposed"], result.Knowledge)
	}
	if trust["service is not exposed"] != harness.KnowledgeRefuted {
		t.Fatalf("loser trust = %q, want refuted (%+v)", trust["service is not exposed"], result.Knowledge)
	}
	reviews := 0
	for _, branch := range result.Branches {
		if branch.Kind != harness.BranchReview {
			continue
		}
		reviews++
		if len(branch.ReviewOf) != 2 {
			t.Fatalf("review branch binds %d ids, want 2: %v", len(branch.ReviewOf), branch.ReviewOf)
		}
		if branch.Status != harness.BranchResolved {
			t.Fatalf("review branch status = %q, want resolved", branch.Status)
		}
	}
	if reviews != 1 {
		t.Fatalf("review branches = %d, want 1", reviews)
	}
}

func TestRefutedKnowledgeIsNeverInjected(t *testing.T) {
	now := time.Now().UTC()
	refuted := harness.Knowledge{ID: "k2", TaskID: "task-1", RunID: "run-1", Statement: "refuted", Trust: harness.KnowledgeRefuted, ValidFrom: now}
	kept := harness.Knowledge{ID: "k1", TaskID: "task-1", RunID: "run-1", Statement: "kept", Trust: harness.KnowledgeCorroborated, ValidFrom: now}
	filtered := harness.FilterKnowledge([]harness.Knowledge{refuted, kept}, harness.KnowledgeQuery{TaskID: "task-1", RunID: "run-1", Now: now})
	if len(filtered) != 1 || filtered[0].ID != "k1" {
		t.Fatalf("filtered = %+v, want only k1", filtered)
	}
	if _, dropped := harness.BuildPlannerContext(harness.Task{ID: "task-1", Objective: "x"}, harness.Target{ID: "target-1"}, nil, []harness.Knowledge{refuted, kept}, harness.Completion{}, nil); dropped != 1 {
		t.Fatalf("digest dropped = %d, want 1", dropped)
	}
}

// conflictReviewPlanner creates the initial hypothesis and its refutation, then
// stops once the review branch exists.
type conflictReviewPlanner struct{}

func (conflictReviewPlanner) Prompt(harness.PlanRequest) (string, error) { return "plan", nil }
func (conflictReviewPlanner) Parse(req harness.PlanRequest, _ harness.TurnResult) (harness.PlanProposal, error) {
	topics := map[string]bool{}
	for _, branch := range req.Branches {
		if branch.Kind == harness.BranchReview {
			return harness.PlanProposal{}, nil
		}
		topics[branch.Topic] = true
	}
	proposal := func(topic string) harness.BranchProposal {
		return harness.BranchProposal{ParentID: "root", Topic: topic, Goal: topic, Hypothesis: topic, Target: "target-1", SuccessCondition: "yes", FailureCondition: "no"}
	}
	switch {
	case !topics["web"]:
		return harness.PlanProposal{CreateBranches: []harness.BranchProposal{proposal("web")}}, nil
	case !topics["web-refute"]:
		return harness.PlanProposal{CreateBranches: []harness.BranchProposal{proposal("web-refute")}}, nil
	}
	return harness.PlanProposal{}, nil
}

// conflictReviewExecutor emits the claim, the refutation and, on the review
// branch, the adjudication for the bound pair.
type conflictReviewExecutor struct{}

func (conflictReviewExecutor) Prompt(harness.AttemptRequest) (string, error) { return "execute", nil }
func (conflictReviewExecutor) Parse(req harness.AttemptRequest, _ harness.TurnResult) (harness.AttemptReport, error) {
	name := "evidence.txt"
	if err := os.WriteFile(filepath.Join(req.WorkspaceDir, name), []byte("evidence"), 0o600); err != nil {
		return harness.AttemptReport{}, err
	}
	report := harness.AttemptReport{
		BranchID: req.Branch.ID, Status: harness.AttemptCompleted, Outcome: harness.AttemptLocalSuccess,
		Observations: []harness.ObservationProposal{{LocalID: "obs-1", Kind: "test.evidence", Summary: "observed", Target: "target-1", EvidenceFiles: []string{name}}},
	}
	proposal := func(topic, statement string) harness.KnowledgeProposal {
		return harness.KnowledgeProposal{Topic: topic, Kind: "test.kind", Statement: statement, Scope: harness.KnowledgeScopeTask, Target: "target-1", ObservationRefs: []string{"obs-1"}, Reuse: harness.ReuseStable}
	}
	switch {
	case strings.HasPrefix(req.Branch.Topic, "review."):
		var winner, loser string
		for _, item := range req.Knowledge {
			switch item.Statement {
			case "service is exposed":
				winner = item.ID
			case "service is not exposed":
				loser = item.ID
			}
		}
		if winner == "" || loser == "" {
			return report, nil
		}
		adjudication := proposal("review.result", "service is exposed")
		adjudication.Supports = []string{winner}
		adjudication.Refutes = []string{loser}
		report.Knowledge = []harness.KnowledgeProposal{adjudication}
	case req.Branch.Topic == "web":
		report.Knowledge = []harness.KnowledgeProposal{proposal("service.exposed", "service is exposed")}
	case req.Branch.Topic == "web-refute":
		refutation := proposal("service.not-exposed", "service is not exposed")
		for _, item := range req.Knowledge {
			if item.Statement == "service is exposed" {
				refutation.Refutes = []string{item.ID}
			}
		}
		report.Knowledge = []harness.KnowledgeProposal{refutation}
	}
	return report, nil
}
