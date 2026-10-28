package core

import (
	"testing"
	"time"
)

func TestFindingFactRequiresObservation(t *testing.T) {
	fact := Fact{
		ID: "fact-1", RunID: "run-1", TaskID: "task-1", Kind: FactFinding,
		Description: "found an exposed service", CreatedAt: time.Now().UTC(),
	}
	if err := ValidateFact(fact); err == nil {
		t.Fatal("finding without observation accepted")
	}
	fact.ObservationIDs = []string{"observation-1"}
	if err := ValidateFact(fact); err != nil {
		t.Fatalf("evidence-backed finding rejected: %v", err)
	}
}

func TestReasonReportCannotExceedFreeSlots(t *testing.T) {
	report := ReasonReport{Intents: []IntentProposal{
		{From: []string{"fact-origin"}, Description: "first"},
		{From: []string{"fact-origin"}, Description: "second"},
		{From: []string{"fact-origin"}, Description: "third"},
	}}
	if err := ValidateReasonReport(report, 2); err == nil {
		t.Fatal("three intents accepted for two free slots")
	}
	report.Intents = report.Intents[:2]
	if err := ValidateReasonReport(report, 2); err != nil {
		t.Fatalf("two intents rejected for two free slots: %v", err)
	}
}

func TestReasonReportRejectsIntentsAndGoalCandidateTogether(t *testing.T) {
	report := ReasonReport{
		Intents:       []IntentProposal{{From: []string{"fact-1"}, Description: "explore"}},
		GoalCandidate: &GoalCandidate{From: []string{"fact-1"}, Description: "goal"},
	}
	if err := ValidateReasonReport(report, 2); err == nil {
		t.Fatal("intents and goal candidate accepted together")
	}
}

func TestIntentTransitions(t *testing.T) {
	valid := [][2]IntentState{
		{"", IntentOpen},
		{IntentOpen, IntentClaimed},
		{IntentClaimed, IntentOpen},
		{IntentClaimed, IntentConcluded},
		{IntentClaimed, IntentAbandoned},
	}
	for _, pair := range valid {
		if err := ValidateIntentTransition(pair[0], pair[1]); err != nil {
			t.Fatalf("valid transition %q -> %q rejected: %v", pair[0], pair[1], err)
		}
	}
	invalid := [][2]IntentState{
		{IntentConcluded, IntentClaimed},
		{IntentAbandoned, IntentOpen},
		{IntentOpen, IntentConcluded},
	}
	for _, pair := range invalid {
		if err := ValidateIntentTransition(pair[0], pair[1]); err == nil {
			t.Fatalf("invalid transition %q -> %q accepted", pair[0], pair[1])
		}
	}
}

func TestExploreReportOnlyProducesFindingOrNegative(t *testing.T) {
	base := ExploreReport{IntentID: "intent-1", Phase: IntentAttemptConcluded}
	for _, kind := range []FactKind{FactOrigin, FactGoal, ""} {
		report := base
		report.Fact = &FactProposal{Kind: kind, Description: "result"}
		if err := ValidateExploreReport(report); err == nil {
			t.Fatalf("explore fact kind %q accepted", kind)
		}
	}
	base.Fact = &FactProposal{Kind: FactFinding, Description: "result"}
	if err := ValidateExploreReport(base); err == nil {
		t.Fatal("finding fact without observation refs accepted")
	}
	base.Fact.ObservationRefs = []string{"obs-1"}
	if err := ValidateExploreReport(base); err != nil {
		t.Fatalf("evidence-backed finding rejected: %v", err)
	}
	base.Fact = &FactProposal{Kind: FactNegative, Description: "nothing found"}
	if err := ValidateExploreReport(base); err != nil {
		t.Fatalf("negative fact rejected: %v", err)
	}
}

func TestBlackboardPolicyDefaults(t *testing.T) {
	policy := DefaultRunPolicy().Exploration
	if policy.MaxOpenIntents != 2 {
		t.Fatalf("MaxOpenIntents = %d, want 2", policy.MaxOpenIntents)
	}
	if policy.MaxTotalIntents <= 0 || policy.MaxAttemptsPerIntent <= 0 {
		t.Fatalf("invalid intent defaults: %+v", policy)
	}
	if policy.ConcludeTimeout <= 0 {
		t.Fatalf("ConcludeTimeout = %s, want positive", policy.ConcludeTimeout)
	}
	if policy.BootstrapEnabled == nil || !*policy.BootstrapEnabled {
		t.Fatalf("BootstrapEnabled = %v, want true", policy.BootstrapEnabled)
	}
}
