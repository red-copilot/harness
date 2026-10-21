package harness

import (
	"strings"
	"testing"
)

func TestParsePlanOutputRejectsMalformedJSON(t *testing.T) {
	if _, err := ParsePlanOutput([]byte("not json")); err == nil {
		t.Fatal("expected malformed planner output to fail")
	}
}

func TestValidatePlanDropsUnauthorizedTarget(t *testing.T) {
	ledger := NewFactLedger()
	validation, err := ValidatePlan(PlanOutput{NextItems: []ExplorerProposal{{
		Question:         "Probe an unrelated host",
		Target:           "192.0.2.10",
		ExpectedEvidence: "service banner",
	}}}, ledger, nil, []Target{{ID: "web", Addresses: []string{"10.0.0.5:80"}}})
	if err != nil {
		t.Fatalf("ValidatePlan: %v", err)
	}
	if len(validation.Accepted) != 0 {
		t.Fatalf("accepted = %d, want 0", len(validation.Accepted))
	}
	if len(validation.Dropped) != 1 || !strings.Contains(validation.Dropped[0].Reason, "unauthorized") {
		t.Fatalf("dropped = %+v", validation.Dropped)
	}
}

func TestValidatePlanDropsDuplicateFingerprint(t *testing.T) {
	target := "10.0.0.5:80"
	proposal := ExplorerProposal{
		Question:         "Check whether /admin exposes a login form",
		Target:           target,
		ExpectedEvidence: "HTTP response containing a login form",
	}
	first, err := ValidatePlan(PlanOutput{NextItems: []ExplorerProposal{proposal}}, NewFactLedger(), nil,
		[]Target{{ID: "web", Addresses: []string{target}}})
	if err != nil {
		t.Fatalf("first ValidatePlan: %v", err)
	}
	if len(first.Accepted) != 1 {
		t.Fatalf("first accepted = %d, want 1", len(first.Accepted))
	}
	second, err := ValidatePlan(PlanOutput{NextItems: []ExplorerProposal{proposal}}, NewFactLedger(), first.Accepted,
		[]Target{{ID: "web", Addresses: []string{target}}})
	if err != nil {
		t.Fatalf("second ValidatePlan: %v", err)
	}
	if len(second.Accepted) != 0 {
		t.Fatalf("second accepted = %d, want 0", len(second.Accepted))
	}
	if len(second.Dropped) != 1 || !strings.Contains(second.Dropped[0].Reason, "duplicate") {
		t.Fatalf("second dropped = %+v", second.Dropped)
	}
}

func TestValidatePlanKeepsOnlyCorroboratedAnchors(t *testing.T) {
	ledger := NewFactLedger()
	merged := ledger.MergeClaims(1, "item-1", "executor-1", []ExplorerClaim{{
		Claim: "admin login exists", Evidence: "GET /admin returned a form", Targets: []string{"10.0.0.5"},
	}})
	if len(merged.Added) != 1 {
		t.Fatalf("added = %v", merged.Added)
	}
	validation, err := ValidatePlan(PlanOutput{NextItems: []ExplorerProposal{{
		Question:         "Test default credentials against the admin form",
		Target:           "10.0.0.5",
		ExpectedEvidence: "successful authenticated response",
		Anchors:          []string{merged.Added[0]},
	}}}, ledger, nil, []Target{{ID: "web", Addresses: []string{"10.0.0.5:80"}}})
	if err != nil {
		t.Fatalf("ValidatePlan: %v", err)
	}
	if len(validation.Accepted) != 1 {
		t.Fatalf("accepted = %d, want 1", len(validation.Accepted))
	}
	if len(validation.Accepted[0].Anchors) != 0 {
		t.Fatalf("observed fact must not be used as anchor: %v", validation.Accepted[0].Anchors)
	}
}

func TestSeedItemsCreatesDeterministicFrontiers(t *testing.T) {
	items := SeedItems([]Target{
		{ID: "web", Addresses: []string{"10.0.0.5:80"}},
		{ID: "files", Addresses: []string{"10.0.0.6:21"}},
	}, "find all flags", 1)
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0].ID == "" || items[0].Fingerprint == "" || items[0].Status != FrontierOpen {
		t.Fatalf("invalid seed item: %+v", items[0])
	}
	if items[0].TargetHint != "web" || items[1].TargetHint != "files" {
		t.Fatalf("target hints = %q, %q", items[0].TargetHint, items[1].TargetHint)
	}
}

func TestSeedItemsWithoutTargetsReturnsEmpty(t *testing.T) {
	if items := SeedItems(nil, "find the flag", 1); len(items) != 0 {
		t.Fatalf("items = %v, want empty", items)
	}
}
