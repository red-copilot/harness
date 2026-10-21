package harness

import (
	"fmt"
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
		ProbeKey:         "tcp.connect",
		Params:           []string{"192.0.2.10:80"},
		ExpectedEvidence: "service banner",
		FailureEvidence:  "connection failure",
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
		ProbeKey:         "http.get",
		Params:           []string{"/admin"},
		ExpectedEvidence: "HTTP response containing a login form",
		FailureEvidence:  "HTTP 404 or a response without a login form",
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

func TestValidatePlanRejectsFrontierWithoutBoundedProbeContract(t *testing.T) {
	targets := []Target{{ID: "web", Addresses: []string{"10.0.0.5:80"}}}
	tests := []struct {
		name     string
		proposal ExplorerProposal
		reason   string
	}{
		{
			name: "missing probe key",
			proposal: ExplorerProposal{
				Question: "Does the HTTP root expose an app?", Target: "web",
				ExpectedEvidence: "HTTP 200", FailureEvidence: "HTTP 404",
			},
			reason: "probe_key",
		},
		{
			name: "missing parameters",
			proposal: ExplorerProposal{
				Question: "Does the HTTP root expose an app?", Target: "web", ProbeKey: "http.get",
				ExpectedEvidence: "HTTP 200", FailureEvidence: "HTTP 404",
			},
			reason: "params",
		},
		{
			name: "missing failure evidence",
			proposal: ExplorerProposal{
				Question: "Does the HTTP root expose an app?", Target: "web", ProbeKey: "http.get",
				Params: []string{"/"}, ExpectedEvidence: "HTTP 200",
			},
			reason: "failure_evidence",
		},
		{
			name: "open ended probe",
			proposal: ExplorerProposal{
				Question: "Discover every web path", Target: "web", ProbeKey: "path_scan",
				Params: []string{"/"}, ExpectedEvidence: "all paths",
				FailureEvidence: "no path found",
			},
			reason: "open-ended",
		},
		{
			name: "wordlist parameter",
			proposal: ExplorerProposal{
				Question: "Does a generated path exist?", Target: "web", ProbeKey: "http.get",
				Params: []string{"/FUZZ"}, ExpectedEvidence: "HTTP 200",
				FailureEvidence: "HTTP 404",
			},
			reason: "literal",
		},
		{
			name: "CIDR parameter",
			proposal: ExplorerProposal{
				Question: "Is a host reachable?", Target: "web", ProbeKey: "tcp.connect",
				Params: []string{"10.0.0.0/24"}, ExpectedEvidence: "connection accepted",
				FailureEvidence: "connection refused",
			},
			reason: "literal",
		},
		{
			name: "port range parameter",
			proposal: ExplorerProposal{
				Question: "Is a port open?", Target: "web", ProbeKey: "tcp.connect",
				Params: []string{"1-65535"}, ExpectedEvidence: "connection accepted",
				FailureEvidence: "connection refused",
			},
			reason: "literal",
		},
		{
			name: "credential action missing password",
			proposal: ExplorerProposal{
				Question: "Does root have an empty password?", Target: "web", ProbeKey: "telnet.login",
				Params: []string{"10.0.0.5:23", "root"}, ExpectedEvidence: "shell prompt",
				FailureEvidence: "login rejected",
			},
			reason: "host, username and password",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			validation, err := ValidatePlan(
				PlanOutput{NextItems: []ExplorerProposal{test.proposal}},
				NewFactLedger(), nil, targets,
			)
			if err != nil {
				t.Fatalf("ValidatePlan: %v", err)
			}
			if len(validation.Accepted) != 0 {
				t.Fatalf("accepted = %+v, want rejected", validation.Accepted)
			}
			if len(validation.Dropped) != 1 || !strings.Contains(validation.Dropped[0].Reason, test.reason) {
				t.Fatalf("dropped = %+v, want reason containing %q", validation.Dropped, test.reason)
			}
		})
	}
}

func TestFrontierFingerprintUsesProbeIdentityInsteadOfProse(t *testing.T) {
	left := FrontierItem{
		Question: "Does the HTTP root expose an app?", TargetHint: "web",
		ProbeKey: "http.get", Params: []string{"/"},
		ExpectedEvidence: "HTTP 200", FailureEvidence: "HTTP 404",
	}
	right := left
	right.Question = "Check the landing page for a web application"
	right.ExpectedEvidence = "an HTML response"
	if FrontierFingerprint(left) != FrontierFingerprint(right) {
		t.Fatal("rephrasing the same probe created a new frontier identity")
	}
	right.Params = []string{"/admin"}
	if FrontierFingerprint(left) == FrontierFingerprint(right) {
		t.Fatal("different probe parameters must have different identities")
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
		ProbeKey:         "credential.login",
		Params:           []string{"/admin", "admin", "admin"},
		ExpectedEvidence: "successful authenticated response",
		FailureEvidence:  "authentication rejected",
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
	}, "find all flags", nil, 1)
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0].ID == "" || items[0].Fingerprint == "" || items[0].Status != FrontierOpen {
		t.Fatalf("invalid seed item: %+v", items[0])
	}
	if items[0].TargetHint != "web" || items[1].TargetHint != "files" {
		t.Fatalf("target hints = %q, %q", items[0].TargetHint, items[1].TargetHint)
	}
	if len(items[0].Params) != 1 || items[0].Params[0] != "10.0.0.5:80" {
		t.Fatalf("seed params = %v, want the authorized address", items[0].Params)
	}
	if items[0].ProbeKey != "service.banner" || items[0].ExpectedEvidence == "" || items[0].FailureEvidence == "" {
		t.Fatalf("seed contract is incomplete: %+v", items[0])
	}
}

func TestSeedItemsQuotesTheTaskBrief(t *testing.T) {
	items := SeedItems(
		[]Target{{ID: "web", Addresses: []string{"10.0.0.5:80"}}},
		"find all flags",
		map[string]string{"challenge_description": "对目标远程登录服务进行安全测试与漏洞挖掘。"},
		1,
	)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if !strings.Contains(items[0].Question, "远程登录服务") {
		t.Fatalf("seed question does not quote the brief: %q", items[0].Question)
	}
}

func TestSeedItemsIsBoundedByWorkerCount(t *testing.T) {
	targets := make([]Target, 0, 6)
	for index := 0; index < 6; index++ {
		targets = append(targets, Target{ID: fmt.Sprintf("t-%d", index), Addresses: []string{fmt.Sprintf("10.0.0.%d:80", index)}})
	}
	if items := SeedItems(targets, "find all flags", nil, 1); len(items) != DefaultExplorationWorkers {
		t.Fatalf("items = %d, want %d", len(items), DefaultExplorationWorkers)
	}
}

func TestSeedItemsWithoutTargetsReturnsEmpty(t *testing.T) {
	if items := SeedItems(nil, "find the flag", nil, 1); len(items) != 0 {
		t.Fatalf("items = %v, want empty", items)
	}
}

func TestPlannerFactDigestPrefersCorroboratedThenNewest(t *testing.T) {
	facts := []Fact{
		{ID: "observed-old", Trust: FactObserved},
		{ID: "corroborated", Trust: FactCorroborated},
		{ID: "observed-new", Trust: FactObserved},
		{ID: "refuted", Trust: FactRefuted},
		{ID: "contested", Trust: FactObserved, Contested: true},
	}
	digest := plannerFactDigest(facts, 3)
	if len(digest) != 3 {
		t.Fatalf("digest = %d, want 3", len(digest))
	}
	if digest[0].ID != "corroborated" {
		t.Fatalf("digest order = %+v", digest)
	}
	if digest[1].ID != "contested" || digest[2].ID != "observed-new" {
		t.Fatalf("digest should prefer contested and newest observations: %+v", digest)
	}
}
