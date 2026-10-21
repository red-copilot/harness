package harness

import (
	"strings"
	"testing"
)

func TestFactLedgerMergeAddsObservedFact(t *testing.T) {
	ledger := NewFactLedger()
	result := ledger.MergeClaims(1, "i-1", "e1", []ExplorerClaim{{
		Claim: "port 80 is open", Evidence: "nmap: 80/tcp open http", Targets: []string{"web"},
	}})
	if len(result.Added) != 1 {
		t.Fatalf("added = %v, want 1 fact", result.Added)
	}
	fact, ok := ledger.Get(result.Added[0])
	if !ok {
		t.Fatalf("fact %q missing", result.Added[0])
	}
	if fact.Trust != FactObserved {
		t.Fatalf("trust = %q, want observed", fact.Trust)
	}
	if len(fact.Sources) != 1 || fact.Sources[0] != "e1" {
		t.Fatalf("sources = %v", fact.Sources)
	}
}

func TestFactLedgerMergeCorroboratesOnExplicitSupport(t *testing.T) {
	ledger := NewFactLedger()
	first := ledger.MergeClaims(1, "i-1", "e1", []ExplorerClaim{{
		Claim: "port 80 is open", Evidence: "nmap: 80/tcp open http", Targets: []string{"web"},
	}})
	id := first.Added[0]
	result := ledger.MergeClaims(2, "i-2", "e2", []ExplorerClaim{{
		Claim: "http service reachable", Evidence: "curl -I returned 200", Supports: []string{id},
	}})
	if len(result.Added) != 0 {
		t.Fatalf("added = %v, want no new facts", result.Added)
	}
	fact, _ := ledger.Get(id)
	if fact.Trust != FactCorroborated {
		t.Fatalf("trust = %q, want corroborated", fact.Trust)
	}
	if len(fact.Sources) != 2 {
		t.Fatalf("sources = %v, want 2 distinct sessions", fact.Sources)
	}
}

func TestFactLedgerMergeCorroboratesIdenticalClaimText(t *testing.T) {
	ledger := NewFactLedger()
	claim := ExplorerClaim{Claim: "Port 80 is open!", Evidence: "nmap output", Targets: []string{"web"}}
	first := ledger.MergeClaims(1, "i-1", "e1", []ExplorerClaim{claim})
	second := ledger.MergeClaims(1, "i-2", "e2", []ExplorerClaim{claim})
	if len(second.Added) != 0 {
		t.Fatalf("added = %v, want merge into existing fact", second.Added)
	}
	fact, _ := ledger.Get(first.Added[0])
	if fact.Trust != FactCorroborated || len(fact.Sources) != 2 {
		t.Fatalf("fact = %+v", fact)
	}
}

func TestFactLedgerRefutationMarksBothContestedWithoutSilentOverwrite(t *testing.T) {
	ledger := NewFactLedger()
	first := ledger.MergeClaims(1, "i-1", "e1", []ExplorerClaim{{
		Claim: "port 80 is open", Evidence: "nmap: 80/tcp open", Targets: []string{"web"},
	}})
	ledger.MergeClaims(1, "i-2", "e2", []ExplorerClaim{{
		Claim: "http reachable", Evidence: "curl 200", Supports: []string{first.Added[0]},
	}})
	result := ledger.MergeClaims(2, "i-3", "e2", []ExplorerClaim{{
		Claim: "port 80 is filtered", Evidence: "nmap -Pn: 80/tcp filtered", Refutes: []string{first.Added[0]},
	}})
	if len(result.Contested) != 1 {
		t.Fatalf("contested = %v, want 1 pair", result.Contested)
	}
	original, _ := ledger.Get(first.Added[0])
	if !original.Contested {
		t.Fatal("original fact must remain and be contested")
	}
	if original.Trust == FactRefuted {
		t.Fatal("refutation must not silently overwrite trust")
	}
	if len(result.Added) != 1 {
		t.Fatalf("added = %v, want the opposing claim as a fact", result.Added)
	}
	opposing, _ := ledger.Get(result.Added[0])
	if !opposing.Contested || opposing.Trust != FactObserved {
		t.Fatalf("opposing = %+v", opposing)
	}
}

func TestFactLedgerRefutationDoesNotPromoteToRefutedImmediately(t *testing.T) {
	ledger := NewFactLedger()
	a := ledger.MergeClaims(1, "i-1", "e1", []ExplorerClaim{{Claim: "a", Evidence: "e", Targets: []string{"t"}}})
	ledger.MergeClaims(2, "i-2", "e2", []ExplorerClaim{{Claim: "b", Evidence: "e", Refutes: []string{a.Added[0]}}})
	for _, fact := range ledger.Facts() {
		if fact.Trust == FactRefuted {
			t.Fatalf("merge must not refute: %+v", fact)
		}
	}
	ledger.MarkRefuted(a.Added[0], "f-9")
	fact, _ := ledger.Get(a.Added[0])
	if fact.Trust != FactRefuted || fact.Contested {
		t.Fatalf("fact = %+v, want refuted and not contested", fact)
	}
	if len(fact.RefutedBy) != 1 || fact.RefutedBy[0] != "f-9" {
		t.Fatalf("refuted_by = %v", fact.RefutedBy)
	}
}

func TestFactLedgerTruncatesClaimAndEvidence(t *testing.T) {
	ledger := NewFactLedger()
	result := ledger.MergeClaims(1, "i-1", "e1", []ExplorerClaim{{
		Claim: strings.Repeat("c", MaxClaimBytes+50), Evidence: strings.Repeat("e", MaxEvidenceBytes+50),
	}})
	fact, _ := ledger.Get(result.Added[0])
	if len(fact.Claim) > MaxClaimBytes || len(fact.Evidence) > MaxEvidenceBytes {
		t.Fatalf("claim=%d evidence=%d", len(fact.Claim), len(fact.Evidence))
	}
}

func TestFactLedgerDropsDanglingReferences(t *testing.T) {
	ledger := NewFactLedger()
	result := ledger.MergeClaims(1, "i-1", "e1", []ExplorerClaim{{
		Claim: "unverified claim", Evidence: "e", Supports: []string{"missing"},
		Refutes: []string{"also-missing"},
	}})
	if len(result.Added) != 1 {
		t.Fatalf("added = %v", result.Added)
	}
	if len(result.Contested) != 0 {
		t.Fatalf("contested = %v", result.Contested)
	}
}

func TestFactLedgerEnforcesLimits(t *testing.T) {
	ledger := NewFactLedger()
	for i := 0; i < MaxFacts+50; i++ {
		ledger.MergeClaims(1, "i-1", "e1", []ExplorerClaim{{
			Claim: "claim " + strings.Repeat("x", i%7) + " number " + itoa(i), Evidence: "e",
			Targets: []string{"t"},
		}})
	}
	if got := len(ledger.Facts()); got > MaxFacts {
		t.Fatalf("facts = %d, want <= %d", got, MaxFacts)
	}
	perTarget := 0
	for _, fact := range ledger.Facts() {
		if fact.Targets[0] == "t" {
			perTarget++
		}
	}
	if perTarget > MaxFactsPerTarget {
		t.Fatalf("target facts = %d, want <= %d", perTarget, MaxFactsPerTarget)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
