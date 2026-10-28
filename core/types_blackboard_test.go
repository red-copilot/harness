package core

import (
	"testing"
	"time"
)

func TestFindingClaimRequiresObservation(t *testing.T) {
	claim := Claim{
		ID: "claim-1", RunID: "run-1", TaskID: "task-1", Kind: ClaimFinding,
		Description: "found an exposed service", CreatedAt: time.Now().UTC(),
	}
	if err := ValidateClaim(claim); err == nil {
		t.Fatal("finding without observation accepted")
	}
	claim.ObservationIDs = []string{"observation-1"}
	if err := ValidateClaim(claim); err != nil {
		t.Fatalf("evidence-backed finding rejected: %v", err)
	}
}

func TestReasonReportCannotExceedFreeSlots(t *testing.T) {
	report := ReasonReport{Intents: []IntentProposal{
		{From: []string{"claim-origin"}, Description: "first"},
		{From: []string{"claim-origin"}, Description: "second"},
		{From: []string{"claim-origin"}, Description: "third"},
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
		Intents:       []IntentProposal{{From: []string{"claim-1"}, Description: "explore"}},
		GoalCandidate: &GoalCandidate{From: []string{"claim-1"}, Description: "goal"},
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

func TestAttemptReportOnlyProducesFindingOrNegative(t *testing.T) {
	base := AttemptReport{IntentID: "intent-1", Phase: IntentAttemptConcluded}
	for _, kind := range []ClaimKind{ClaimOrigin, ClaimGoal, ""} {
		report := base
		report.Claim = &ClaimProposal{Kind: kind, Description: "result"}
		if err := ValidateAttemptReport(report); err == nil {
			t.Fatalf("explore claim kind %q accepted", kind)
		}
	}
	base.Claim = &ClaimProposal{Kind: ClaimFinding, Description: "result"}
	if err := ValidateAttemptReport(base); err == nil {
		t.Fatal("finding claim without observation refs accepted")
	}
	base.Claim.ObservationRefs = []string{"obs-1"}
	if err := ValidateAttemptReport(base); err != nil {
		t.Fatalf("evidence-backed finding rejected: %v", err)
	}
	base.Claim = &ClaimProposal{Kind: ClaimNegative, Description: "nothing found"}
	if err := ValidateAttemptReport(base); err != nil {
		t.Fatalf("negative claim rejected: %v", err)
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
