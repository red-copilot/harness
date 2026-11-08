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

func TestNegativeClaimRequiresObservation(t *testing.T) {
	claim := Claim{ID: "claim-1", RunID: "run-1", TaskID: "task-1", Kind: ClaimNegative, Description: "nothing found", CreatedAt: time.Now().UTC()}
	if err := ValidateClaim(claim); err == nil {
		t.Fatal("negative without observation accepted")
	}
	claim.ObservationIDs = []string{"observation-1"}
	if err := ValidateClaim(claim); err != nil {
		t.Fatalf("evidence-backed negative rejected: %v", err)
	}
}

func TestReasonReportCannotExceedSingleSlot(t *testing.T) {
	report := ReasonReport{Intents: []IntentProposal{
		{From: []string{"claim-origin"}, Description: "first"},
		{From: []string{"claim-origin"}, Description: "second"},
	}}
	if err := ValidateReasonReport(report, true); err == nil {
		t.Fatal("two intents accepted for one slot")
	}
	report.Intents = report.Intents[:1]
	if err := ValidateReasonReport(report, true); err != nil {
		t.Fatalf("one intent rejected for one slot: %v", err)
	}
	if err := ValidateReasonReport(report, false); err == nil {
		t.Fatal("intent accepted while the slot is occupied")
	}
}

func TestReasonReportRejectsIntentsAndGoalCandidateTogether(t *testing.T) {
	report := ReasonReport{
		Intents:       []IntentProposal{{From: []string{"claim-1"}, Description: "explore"}},
		GoalCandidate: &GoalCandidate{From: []string{"claim-1"}, Description: "goal"},
	}
	if err := ValidateReasonReport(report, true); err == nil {
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
	base.Observations = []ObservationProposal{{LocalID: "obs-1", Kind: "test", Summary: "observed"}}
	if err := ValidateAttemptReport(base); err != nil {
		t.Fatalf("evidence-backed finding rejected: %v", err)
	}
	base.Claim.ObservationRefs = []string{"missing"}
	if err := ValidateAttemptReport(base); err == nil {
		t.Fatal("finding claim with unknown observation ref accepted")
	}
	base.Claim.ObservationRefs = []string{"obs-1"}
	base.Observations = append(base.Observations, base.Observations[0])
	if err := ValidateAttemptReport(base); err == nil {
		t.Fatal("duplicate observation local_id accepted")
	}
	base.Observations = base.Observations[:1]
	base.Claim = &ClaimProposal{Kind: ClaimNegative, Description: "nothing found"}
	if err := ValidateAttemptReport(base); err == nil {
		t.Fatal("negative claim without observation refs accepted")
	}
	base.Claim.ObservationRefs = []string{"obs-1"}
	if err := ValidateAttemptReport(base); err != nil {
		t.Fatalf("evidence-backed negative claim rejected: %v", err)
	}
}

func TestBlackboardPolicyDefaults(t *testing.T) {
	policy := DefaultRunPolicy().Exploration
	if policy.MaxAuthorityObserveFailures <= 0 {
		t.Fatalf("MaxAuthorityObserveFailures = %d, want positive", policy.MaxAuthorityObserveFailures)
	}
	if policy.MaxTotalIntents <= 0 || policy.MaxAttemptsPerIntent <= 0 {
		t.Fatalf("invalid intent defaults: %+v", policy)
	}
	if policy.ConcludeTimeout <= 0 {
		t.Fatalf("ConcludeTimeout = %s, want positive", policy.ConcludeTimeout)
	}
}
