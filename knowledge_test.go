package harness_test

import (
	"strings"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

func TestKnowledgeRequiresObservationFromCurrentAttempt(t *testing.T) {
	req := knowledgeRequest()
	req.Proposal.ObservationRefs = []string{"missing"}
	if _, _, err := harness.PromoteKnowledge(req); err == nil || !strings.Contains(err.Error(), "current attempt") {
		t.Fatalf("err = %v", err)
	}
}

func TestCorroborationRequiresDistinctExecutorSessions(t *testing.T) {
	base := knowledgeRequest()
	existing := harness.Knowledge{ID: "k1", TaskID: base.Task.ID, RunID: "old-run", Topic: "service.exposed", Kind: "adapter.kind", Statement: "service exposed", Scope: harness.KnowledgeScopeTask, Trust: harness.KnowledgeObserved, Reuse: harness.ReuseStable, ValidFrom: base.Now, Sources: []harness.KnowledgeSource{{RunID: "old-run", AttemptID: "a0", SessionID: "session-1"}}, Version: 1}
	req := base
	req.Proposal.Topic = "service.exposed.support"
	req.Proposal.Supports = []string{"k1"}
	req.SessionID = "session-1"
	req.Existing = []harness.Knowledge{existing}
	got, _, err := harness.PromoteKnowledge(req)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "k1" || got.Trust != harness.KnowledgeObserved {
		t.Fatalf("same session changed trust: %+v", got)
	}
	req.SessionID = "session-2"
	got, _, err = harness.PromoteKnowledge(req)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "k1" || got.Trust != harness.KnowledgeCorroborated {
		t.Fatalf("distinct session trust: %+v", got)
	}
}

func TestRefuteCreatesContestedPairAndReviewBranch(t *testing.T) {
	req := knowledgeRequest()
	existing := harness.Knowledge{ID: "k1", TaskID: req.Task.ID, RunID: "old-run", Topic: "service.exposed", Kind: "adapter.kind", Statement: "service exposed", Scope: harness.KnowledgeScopeTask, Trust: harness.KnowledgeObserved, Reuse: harness.ReuseStable, ValidFrom: req.Now}
	req.Proposal.Topic = "service.not-exposed"
	req.Proposal.Refutes = []string{"k1"}
	req.Existing = []harness.Knowledge{existing}
	got, events, err := harness.PromoteKnowledge(req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Trust != harness.KnowledgeContested || len(got.Contradicts) != 2 || !contains(got.Contradicts, "k1") || !contains(got.Contradicts, got.ID) {
		t.Fatalf("contested pair = %+v", got)
	}
	found := false
	for _, event := range events {
		if event.Type == "knowledge.review_required" {
			found = true
		}
	}
	if !found {
		t.Fatalf("events = %+v", events)
	}
}

func TestReviewResolutionChangesOnlyBoundPair(t *testing.T) {
	winner, loser, err := harness.ResolveReview([]string{"k2", "k1"}, []harness.KnowledgeProposal{{Supports: []string{"k2"}, Refutes: []string{"k1"}}})
	if err != nil || winner != "k2" || loser != "k1" {
		t.Fatalf("winner=%q loser=%q err=%v", winner, loser, err)
	}
	if _, _, err := harness.ResolveReview([]string{"k1", "k2"}, []harness.KnowledgeProposal{{Supports: []string{"k3"}, Refutes: []string{"k1"}}}); err == nil {
		t.Fatal("mismatched review accepted")
	}
}

func TestReviewInconclusiveLeavesContested(t *testing.T) {
	if _, _, err := harness.ResolveReview([]string{"k1", "k2"}, nil); err == nil {
		t.Fatal("empty review resolved")
	}
}

func TestCrossRunReuseRespectsTaskTargetScopeAndReuse(t *testing.T) {
	now := time.Now().UTC()
	query := harness.KnowledgeQuery{TaskID: "task-1", RunID: "run-new", TargetFingerprint: "target-1", AllowCrossRun: true, Now: now}
	items := []harness.Knowledge{
		{ID: "stable", TaskID: "task-1", RunID: "run-old", Target: "target-1", Scope: harness.KnowledgeScopeTask, Reuse: harness.ReuseStable, Trust: harness.KnowledgeObserved, ValidFrom: now},
		{ID: "never", TaskID: "task-1", RunID: "run-old", Target: "target-1", Scope: harness.KnowledgeScopeTask, Reuse: harness.ReuseNever, Trust: harness.KnowledgeObserved, ValidFrom: now},
		{ID: "same-run-never", TaskID: "task-1", RunID: "run-new", Target: "target-1", Scope: harness.KnowledgeScopeTask, Reuse: harness.ReuseNever, Trust: harness.KnowledgeObserved, ValidFrom: now},
		{ID: "instance", TaskID: "task-1", RunID: "run-old", Target: "target-1", Scope: harness.KnowledgeScopeInstance, Reuse: harness.ReuseInstance, Trust: harness.KnowledgeObserved, ValidFrom: now},
		{ID: "other-target", TaskID: "task-1", RunID: "run-old", Target: "target-2", Scope: harness.KnowledgeScopeTarget, Reuse: harness.ReuseStable, Trust: harness.KnowledgeObserved, ValidFrom: now},
		{ID: "other-task", TaskID: "task-2", RunID: "run-old", Target: "target-1", Scope: harness.KnowledgeScopeTask, Reuse: harness.ReuseStable, Trust: harness.KnowledgeObserved, ValidFrom: now},
	}
	got := harness.FilterKnowledge(items, query)
	ids := map[string]bool{}
	for _, item := range got {
		ids[item.ID] = true
	}
	if !ids["stable"] || !ids["same-run-never"] || !ids["instance"] {
		t.Fatalf("missing expected: %+v", ids)
	}
	if ids["never"] || ids["other-target"] || ids["other-task"] {
		t.Fatalf("leaked filtered entries: %+v", ids)
	}
}

func TestExpiredKnowledgeIsExcludedAndAudited(t *testing.T) {
	now := time.Now().UTC()
	expired := now.Add(-time.Minute)
	items := []harness.Knowledge{{ID: "expired", TaskID: "task-1", RunID: "run", Scope: harness.KnowledgeScopeInstance, Target: "target-1", Reuse: harness.ReuseInstance, Trust: harness.KnowledgeObserved, ValidFrom: now.Add(-time.Hour), ExpiresAt: &expired}}
	got := harness.FilterKnowledge(items, harness.KnowledgeQuery{TaskID: "task-1", RunID: "run", TargetFingerprint: "target-1", Now: now})
	if len(got) != 0 {
		t.Fatalf("expired knowledge returned: %+v", got)
	}
}

func TestKnowledgeLimitIsMechanical(t *testing.T) {
	req := knowledgeRequest()
	for i := 0; i < 200; i++ {
		req.Existing = append(req.Existing, harness.Knowledge{ID: "old-" + string(rune(i+1)), TaskID: req.Task.ID, RunID: "old", Topic: "old.topic." + string(rune(i+1)), ValidFrom: req.Now})
	}
	if _, _, err := harness.PromoteKnowledge(req); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v", err)
	}
}

func TestKnowledgeFingerprintRejectsDuplicate(t *testing.T) {
	req := knowledgeRequest()
	existing := harness.Knowledge{ID: "k1", TaskID: req.Task.ID, RunID: "old", Topic: " service.exposed ", Scope: harness.KnowledgeScopeTask, Kind: "adapter.kind", Target: "target-1", ValidFrom: req.Now}
	req.Proposal.Topic = "service.exposed"
	req.Proposal.Scope = harness.KnowledgeScopeTask
	req.Proposal.Kind = "adapter.kind"
	req.Proposal.Target = "target-1"
	req.Existing = []harness.Knowledge{existing}
	if _, _, err := harness.PromoteKnowledge(req); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("err = %v", err)
	}
}

func knowledgeRequest() harness.KnowledgePromotionRequest {
	now := time.Now().UTC()
	obs := harness.Observation{ID: "attempt-1:obs:obs-1", RunID: "run-1", BranchID: "branch-1", AttemptID: "attempt-1"}
	return harness.KnowledgePromotionRequest{RunID: "run-1", AttemptID: "attempt-1", SessionID: "session-1", Task: harness.Task{ID: "task-1", Objective: "x"}, Target: harness.Target{ID: "target-1", Isolation: harness.IsolationCallerManaged}, Branch: harness.Branch{ID: "branch-1", TaskID: "task-1", RunID: "run-1"}, Proposal: harness.KnowledgeProposal{Topic: "service.exposed", Kind: "adapter.kind", Statement: "service is exposed", Scope: harness.KnowledgeScopeTask, Target: "target-1", ObservationRefs: []string{"obs-1"}, Reuse: harness.ReuseStable}, Observations: map[string]harness.Observation{"obs-1": obs}, Now: now}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
