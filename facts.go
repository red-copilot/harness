package harness

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxFacts          = 200
	MaxItems          = 100
	MaxFactsPerTarget = 40
	MaxClaimBytes     = 300
	MaxEvidenceBytes  = 400
)

// FactLedger is the bounded, process-local fact store shared by the controller
// loop. It is an exploration aid, never a success authority.
type FactLedger struct {
	byID  map[string]*Fact
	order []string
	seq   int
}

// MergeResult describes what one explorer report changed in the ledger.
type MergeResult struct {
	Added        []string
	Corroborated []string
	Contested    [][2]string
	Updated      []string
}

// NewFactLedger creates an empty ledger.
func NewFactLedger() *FactLedger {
	return &FactLedger{byID: map[string]*Fact{}}
}

// Facts returns a copy of every fact in insertion order.
func (l *FactLedger) Facts() []Fact {
	out := make([]Fact, 0, len(l.order))
	for _, id := range l.order {
		out = append(out, copyFact(*l.byID[id]))
	}
	return out
}

// FactsByID returns copies of the named facts, skipping unknown ids and
// duplicates. It is the bounded projection used for UI/audit events.
func (l *FactLedger) FactsByID(ids ...[]string) []Fact {
	out := make([]Fact, 0, 8)
	seen := map[string]struct{}{}
	for _, group := range ids {
		for _, id := range group {
			if _, ok := seen[id]; ok {
				continue
			}
			fact, ok := l.Get(id)
			if !ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, fact)
		}
	}
	return out
}

// Get returns a copy of one fact.
func (l *FactLedger) Get(id string) (Fact, bool) {
	fact, ok := l.byID[id]
	if !ok {
		return Fact{}, false
	}
	return copyFact(*fact), true
}

// Corroborated returns copies of all corroborated facts in insertion order.
func (l *FactLedger) Corroborated() []Fact {
	var out []Fact
	for _, id := range l.order {
		if l.byID[id].Trust == FactCorroborated {
			out = append(out, copyFact(*l.byID[id]))
		}
	}
	return out
}

// MergeClaims folds one explorer report's claims into the ledger. Explicit
// supports/refutes references are the only merge signal, plus exact normalized
// claim equality within the same targets.
func (l *FactLedger) MergeClaims(round int, itemID, sessionID string, claims []ExplorerClaim) MergeResult {
	var result MergeResult
	for _, claim := range claims {
		text := truncateBytes(strings.TrimSpace(claim.Claim), MaxClaimBytes)
		evidence := truncateBytes(strings.TrimSpace(claim.Evidence), MaxEvidenceBytes)
		if text == "" || evidence == "" {
			continue
		}
		supports := l.knownIDs(claim.Supports)
		refutes := l.knownIDs(claim.Refutes)
		targets := uniqueStrings(claim.Targets)

		if len(supports) > 0 {
			for _, id := range supports {
				if l.addSource(id, sessionID) {
					result.Corroborated = appendUnique(result.Corroborated, id)
				}
			}
			continue
		}

		fact := l.findByClaim(text, targets)
		if fact == nil {
			fact = &Fact{
				ID: fmt.Sprintf("f-%d-%d", round, l.nextSeq()), Claim: text, Evidence: evidence,
				Source: fmt.Sprintf("%s#%s", sessionID, itemID), Sources: []string{sessionID},
				Trust: FactObserved, Targets: targets,
			}
			l.byID[fact.ID] = fact
			l.order = append(l.order, fact.ID)
			result.Added = append(result.Added, fact.ID)
		} else if l.addSource(fact.ID, sessionID) {
			result.Corroborated = appendUnique(result.Corroborated, fact.ID)
		}

		for _, id := range refutes {
			other := l.byID[id]
			other.Contested = true
			fact.Contested = true
			result.Contested = append(result.Contested, [2]string{other.ID, fact.ID})
			result.Updated = appendUnique(result.Updated, other.ID, fact.ID)
		}
	}
	l.enforceLimits()
	return result
}

// MarkRefuted is the only way a fact becomes refuted: an explicit controller
// resolution after a conflict review.
func (l *FactLedger) MarkRefuted(factID, byFactID string) {
	fact, ok := l.byID[factID]
	if !ok {
		return
	}
	fact.Trust = FactRefuted
	fact.Contested = false
	if byFactID != "" && !contains(fact.RefutedBy, byFactID) {
		fact.RefutedBy = append(fact.RefutedBy, byFactID)
	}
}

func (l *FactLedger) addSource(id, sessionID string) bool {
	fact, ok := l.byID[id]
	if !ok || sessionID == "" {
		return false
	}
	if contains(fact.Sources, sessionID) {
		return false
	}
	fact.Sources = append(fact.Sources, sessionID)
	if fact.Trust == FactObserved && len(fact.Sources) >= 2 {
		fact.Trust = FactCorroborated
		return true
	}
	return false
}

func (l *FactLedger) knownIDs(ids []string) []string {
	var out []string
	for _, id := range ids {
		if _, ok := l.byID[id]; ok {
			out = appendUnique(out, id)
		}
	}
	return out
}

func (l *FactLedger) findByClaim(claim string, targets []string) *Fact {
	normalized := normalizeClaim(claim)
	for _, id := range l.order {
		candidate := l.byID[id]
		if normalizeClaim(candidate.Claim) != normalized {
			continue
		}
		if sameTargets(candidate.Targets, targets) {
			return candidate
		}
	}
	return nil
}

func (l *FactLedger) nextSeq() int {
	l.seq++
	return l.seq
}

func (l *FactLedger) enforceLimits() {
	for _, target := range l.allTargets() {
		for countFactsForTarget(l, target) > MaxFactsPerTarget {
			if !l.removeOldest(target, false) {
				break
			}
		}
	}
	for len(l.order) > MaxFacts {
		if !l.removeOldest("", true) {
			break
		}
	}
}

func (l *FactLedger) allTargets() []string {
	var targets []string
	for _, id := range l.order {
		for _, target := range l.byID[id].Targets {
			targets = appendUnique(targets, target)
		}
	}
	return targets
}

func (l *FactLedger) removeOldest(target string, global bool) bool {
	for _, id := range l.order {
		fact := l.byID[id]
		if fact.Trust == FactCorroborated || fact.Contested {
			continue
		}
		if !global && !contains(fact.Targets, target) {
			continue
		}
		l.delete(id)
		return true
	}
	for _, id := range l.order {
		fact := l.byID[id]
		if !global && !contains(fact.Targets, target) {
			continue
		}
		l.delete(id)
		return true
	}
	return false
}

func (l *FactLedger) delete(id string) {
	delete(l.byID, id)
	for i, existing := range l.order {
		if existing == id {
			l.order = append(l.order[:i], l.order[i+1:]...)
			return
		}
	}
}

func countFactsForTarget(l *FactLedger, target string) int {
	count := 0
	for _, id := range l.order {
		if contains(l.byID[id].Targets, target) {
			count++
		}
	}
	return count
}

func copyFact(fact Fact) Fact {
	fact.Sources = append([]string(nil), fact.Sources...)
	fact.Targets = append([]string(nil), fact.Targets...)
	fact.RefutedBy = append([]string(nil), fact.RefutedBy...)
	return fact
}

func normalizeClaim(claim string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(claim) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func sameTargets(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for _, target := range left {
		if !contains(right, target) {
			return false
		}
	}
	return true
}

func appendUnique(values []string, additions ...string) []string {
	for _, addition := range additions {
		if addition == "" || contains(values, addition) {
			continue
		}
		values = append(values, addition)
	}
	return values
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func truncateBytes(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}
