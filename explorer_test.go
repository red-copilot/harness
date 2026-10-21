package harness

import (
	"encoding/json"
	"strings"
	"testing"
)

func testItem() FrontierItem {
	return FrontierItem{
		ID: "i-1", Question: "which HTTP paths are exposed?", TargetHint: "10.0.0.5:80",
		ProbeKey: "http.get", Params: []string{"/"},
		ExpectedEvidence: "HTTP status codes for the requested path", FailureEvidence: "HTTP 404",
		MaxTools: DefaultFrontierToolLimit, MaxDurationSeconds: DefaultFrontierDurationSeconds,
		Status: FrontierOpen, Round: 1,
	}
}

func TestParseExplorerReportRejectsMismatchedItemID(t *testing.T) {
	data, _ := json.Marshal(ExplorerReport{
		ItemID: "i-9", Verdict: VerdictFound,
		Claims: []ExplorerClaim{{Claim: "c", Evidence: "e"}},
	})
	if _, err := ParseExplorerReport(data, testItem()); err == nil || !strings.Contains(err.Error(), "item_id") {
		t.Fatalf("err = %v, want item_id mismatch", err)
	}
}

func TestParseExplorerReportRejectsInvalidVerdict(t *testing.T) {
	data, _ := json.Marshal(ExplorerReport{ItemID: "i-1", Verdict: "maybe"})
	if _, err := ParseExplorerReport(data, testItem()); err == nil || !strings.Contains(err.Error(), "verdict") {
		t.Fatalf("err = %v, want invalid verdict", err)
	}
}

func TestParseExplorerReportAcceptsClosedExperimentVerdicts(t *testing.T) {
	for _, verdict := range []string{VerdictSupported, VerdictRefuted, VerdictBlocked, VerdictInconclusive} {
		t.Run(verdict, func(t *testing.T) {
			report := ExplorerReport{ItemID: "i-1", Verdict: verdict}
			if verdict == VerdictSupported || verdict == VerdictRefuted {
				report.Claims = []ExplorerClaim{{Claim: "observed result", Evidence: "bounded probe output"}}
			}
			data, _ := json.Marshal(report)
			if _, err := ParseExplorerReport(data, testItem()); err != nil {
				t.Fatalf("verdict %q rejected: %v", verdict, err)
			}
		})
	}
}

func TestParseExplorerReportRequiresEvidenceForSupportedOrRefuted(t *testing.T) {
	for _, verdict := range []string{VerdictSupported, VerdictRefuted} {
		t.Run(verdict, func(t *testing.T) {
			data, _ := json.Marshal(ExplorerReport{ItemID: "i-1", Verdict: verdict})
			if _, err := ParseExplorerReport(data, testItem()); err == nil || !strings.Contains(err.Error(), "claim") {
				t.Fatalf("err = %v, want claim evidence error", err)
			}
		})
	}
}

func TestParseExplorerReportRejectsMalformedJSON(t *testing.T) {
	if _, err := ParseExplorerReport([]byte("not json"), testItem()); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestParseExplorerReportDropsClaimsWithoutEvidence(t *testing.T) {
	data, _ := json.Marshal(ExplorerReport{
		ItemID: "i-1", Verdict: VerdictFound,
		Claims: []ExplorerClaim{
			{Claim: "kept", Evidence: "proof"},
			{Claim: "no evidence"},
			{Evidence: "no claim"},
		},
	})
	report, err := ParseExplorerReport(data, testItem())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Claims) != 1 || report.Claims[0].Claim != "kept" {
		t.Fatalf("claims = %+v", report.Claims)
	}
}

func TestParseExplorerReportCapsProposalsAtThree(t *testing.T) {
	var next []ExplorerProposal
	for i := 0; i < 5; i++ {
		next = append(next, ExplorerProposal{Question: "q" + itoa(i), Target: "10.0.0.5"})
	}
	next = append(next, ExplorerProposal{Question: "", Target: "empty"})
	data, _ := json.Marshal(ExplorerReport{ItemID: "i-1", Verdict: VerdictInconclusive, Next: next})
	report, err := ParseExplorerReport(data, testItem())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Next) != MaxProposals {
		t.Fatalf("next = %d, want %d", len(report.Next), MaxProposals)
	}
	for _, proposal := range report.Next {
		if proposal.Question == "" {
			t.Fatal("empty question must be dropped")
		}
	}
}

func TestParseExplorerReportDropsMalformedNextProposals(t *testing.T) {
	data := []byte(`{
		"item_id":"i-1",
		"verdict":"supported",
		"claims":[{"claim":"telnet reachable","evidence":"IAC banner returned"}],
		"next":[
			"finish telnet negotiation",
			{"question":"Does the negotiated banner include a login prompt?","target":"10.0.0.5","probe_key":"service.banner","params":["10.0.0.5:23"],"expected_evidence":"login prompt","failure_evidence":"no prompt"}
		]
	}`)
	report, err := ParseExplorerReport(data, testItem())
	if err != nil {
		t.Fatalf("ParseExplorerReport: %v", err)
	}
	if len(report.Claims) != 1 {
		t.Fatalf("claims = %+v, want the valid claim retained", report.Claims)
	}
	if len(report.Next) != 1 || report.Next[0].Question == "" {
		t.Fatalf("next = %+v, want only the valid proposal", report.Next)
	}
}

func TestBuildExplorerBriefContainsExactlyOneItem(t *testing.T) {
	facts := []Fact{
		{ID: "f-1", Claim: "port 80 open", Evidence: "nmap", Trust: FactCorroborated, Targets: []string{"10.0.0.5"}},
		{ID: "f-2", Claim: "unrelated", Evidence: "x", Trust: FactObserved, Targets: []string{"other"}},
	}
	item := testItem()
	item.ProbeKey = "http.get"
	item.Params = []string{"/"}
	item.FailureEvidence = "HTTP 404"
	brief := BuildExplorerBrief("find the flag", []Target{{ID: "web", Addresses: []string{"10.0.0.5:80"}}}, item, facts)
	for _, want := range []string{
		"Task objective: find the flag", "which HTTP paths are exposed?", "http.get", "HTTP 404",
		"report.json", "corroborated", "10.0.0.5", "8 tool", "90s",
		"one matching tool reference", "only when a concrete candidate flag exists",
		`"next":[{"question"`, "简体中文", "xxd -r -p",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief missing %q:\n%s", want, brief)
		}
	}
	if strings.Contains(brief, "unrelated") {
		t.Fatalf("brief leaked unrelated fact:\n%s", brief)
	}
}

func TestBuildExplorerBriefShowsOnlyTheAssignedTarget(t *testing.T) {
	brief := BuildExplorerBrief(
		"find the flag",
		[]Target{
			{ID: "web", Addresses: []string{"10.0.0.5:80"}},
			{ID: "files", Addresses: []string{"10.0.0.6:21"}},
		},
		testItem(),
		nil,
	)
	if strings.Contains(brief, "10.0.0.6") || strings.Contains(brief, "files") {
		t.Fatalf("brief exposed an unassigned target:\n%s", brief)
	}
}

func TestDigestFactsIsBoundedAndExcludesRefuted(t *testing.T) {
	item := testItem()
	facts := []Fact{{ID: "f-refuted", Claim: "refuted", Trust: FactRefuted, Targets: []string{"10.0.0.5"}}}
	for i := 0; i < MaxDigestFacts+5; i++ {
		facts = append(facts, Fact{
			ID: "f-" + itoa(i), Claim: "claim " + itoa(i), Trust: FactObserved,
			Targets: []string{"10.0.0.5"},
		})
	}
	facts = append(facts, Fact{ID: "f-contested", Claim: "contested claim", Trust: FactObserved, Contested: true, Targets: []string{"10.0.0.5"}})
	digest := DigestFacts(item, facts)
	if len(digest) > MaxDigestFacts {
		t.Fatalf("digest = %d, want <= %d", len(digest), MaxDigestFacts)
	}
	for _, fact := range digest {
		if fact.Trust == FactRefuted {
			t.Fatalf("refuted fact in digest: %+v", fact)
		}
	}
}

func TestDigestFactsOnlyIncludesRelatedFacts(t *testing.T) {
	facts := []Fact{
		{ID: "f-related", Claim: "related", Targets: []string{"10.0.0.5"}},
		{ID: "f-anchor", Claim: "anchored", Targets: []string{"other"}},
		{ID: "f-unrelated", Claim: "unrelated", Targets: []string{"elsewhere"}},
	}
	item := testItem()
	item.Anchors = []string{"f-anchor"}
	digest := DigestFacts(item, facts)
	ids := map[string]bool{}
	for _, fact := range digest {
		ids[fact.ID] = true
	}
	if !ids["f-related"] || !ids["f-anchor"] || ids["f-unrelated"] {
		t.Fatalf("digest ids = %v", ids)
	}
}

func TestAuthorizeTargetAcceptsDerivedHostPort(t *testing.T) {
	targets := []Target{{ID: "web", Addresses: []string{"10.0.0.5:80"}}}
	for _, target := range []string{"web", "10.0.0.5", "10.0.0.5:8080", "10.0.0.5/admin"} {
		if !AuthorizeTarget(target, targets) {
			t.Fatalf("target %q should be authorized", target)
		}
	}
}

func TestAuthorizeTargetRejectsUnrelatedHost(t *testing.T) {
	targets := []Target{{ID: "web", Addresses: []string{"10.0.0.5:80"}}}
	for _, target := range []string{"10.0.0.6", "example.com", ""} {
		if AuthorizeTarget(target, targets) {
			t.Fatalf("target %q must not be authorized", target)
		}
	}
}

func TestAuthorizeTargetAcceptsPlannerLabelAndAddress(t *testing.T) {
	targets := []Target{{ID: "web", Addresses: []string{"10.0.0.5:80"}}}
	for _, target := range []string{"web 10.0.0.5:80", "10.0.0.5 web", "web: 10.0.0.5:80"} {
		if !AuthorizeTarget(target, targets) {
			t.Fatalf("planner target %q should be authorized", target)
		}
	}
}

func TestDigestFactsPrefersCorroboratedThenNewestObserved(t *testing.T) {
	item := testItem()
	digest := DigestFacts(item, []Fact{
		{ID: "a-old", Claim: "old", Trust: FactObserved, Targets: []string{"10.0.0.5"}},
		{ID: "corroborated", Claim: "strong", Trust: FactCorroborated, Targets: []string{"10.0.0.5"}},
		{ID: "z-new", Claim: "new", Trust: FactObserved, Targets: []string{"10.0.0.5"}},
	})
	if len(digest) != 3 {
		t.Fatalf("digest = %d, want 3", len(digest))
	}
	if digest[0].ID != "corroborated" || digest[1].ID != "z-new" || digest[2].ID != "a-old" {
		t.Fatalf("digest order = %+v", digest)
	}
}
