package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// PromoteKnowledge applies code-owned trust transitions.
func PromoteKnowledge(req KnowledgePromotionRequest) (Knowledge, []KnowledgeEvent, error) {
	if req.Task.ID == "" {
		return Knowledge{}, nil, errors.New("knowledge task id is required")
	}
	if len(req.Observations) == 0 {
		return Knowledge{}, nil, errors.New("knowledge requires archived observations")
	}
	if req.Proposal.Topic == "" || req.Proposal.Statement == "" {
		return Knowledge{}, nil, errors.New("knowledge topic and statement are required")
	}
	if containsSecretLikeValue(req.Proposal.Statement) {
		return Knowledge{}, nil, errors.New("knowledge statement contains a secret value")
	}
	for _, ref := range req.Proposal.ObservationRefs {
		if _, ok := req.Observations[ref]; !ok {
			return Knowledge{}, nil, fmt.Errorf("knowledge observation %q is not in current attempt", ref)
		}
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	sources := make([]KnowledgeSource, 0, len(req.Proposal.ObservationRefs))
	for _, ref := range req.Proposal.ObservationRefs {
		sources = append(sources, KnowledgeSource{RunID: req.RunID, AttemptID: req.AttemptID, SessionID: req.SessionID, Observation: req.Observations[ref].ID})
	}
	if len(req.Proposal.Supports) == 1 && len(req.Proposal.Refutes) == 0 {
		existing := findKnowledge(req.Existing, req.Proposal.Supports[0])
		if existing == nil {
			return Knowledge{}, nil, fmt.Errorf("knowledge support target %q is unknown", req.Proposal.Supports[0])
		}
		updated := *existing
		updated.Sources = appendUniqueSource(updated.Sources, sources...)
		updated.LastValidated = now
		if updated.Trust != KnowledgeContested && distinctSessions(updated.Sources) >= 2 {
			updated.Trust = KnowledgeCorroborated
		}
		updated.Version++
		return updated, []KnowledgeEvent{{Type: "knowledge.supported", Data: map[string]any{"knowledge_id": updated.ID, "sources": sources}}}, nil
	}
	if len(req.Existing) >= 200 {
		return Knowledge{}, nil, errors.New("knowledge limit exceeded")
	}
	candidateTarget := req.Proposal.Target
	if candidateTarget == "" {
		candidateTarget = req.Target.ID
	}
	candidate := Knowledge{TaskID: req.Task.ID, RunID: req.RunID, Topic: normalizeKnowledgeTopic(req.Proposal.Topic), Kind: req.Proposal.Kind, Statement: req.Proposal.Statement, Scope: req.Proposal.Scope, Target: candidateTarget, Trust: KnowledgeObserved, Reuse: req.Proposal.Reuse, Sources: sources, ValidFrom: now, LastValidated: now, ExpiresAt: req.Proposal.ExpiresAt, Version: 1}
	if candidate.Scope == "" {
		candidate.Scope = KnowledgeScopeTask
	}
	if candidate.Reuse == "" {
		candidate.Reuse = ReuseNever
	}
	candidate.ID = knowledgeID(candidate, 1)
	fingerprint := KnowledgeFingerprint(candidate)
	for _, existing := range req.Existing {
		if KnowledgeFingerprint(existing) == fingerprint && existing.TaskID == candidate.TaskID {
			return Knowledge{}, nil, fmt.Errorf("duplicate knowledge fingerprint %s", fingerprint)
		}
	}
	var events []KnowledgeEvent
	for _, refute := range req.Proposal.Refutes {
		existing := findKnowledge(req.Existing, refute)
		if existing == nil {
			return Knowledge{}, nil, fmt.Errorf("knowledge refute target %q is unknown", refute)
		}
		candidate.Contradicts = uniqueSorted(append(candidate.Contradicts, existing.ID))
		candidate.Trust = KnowledgeContested
		events = append(events, KnowledgeEvent{Type: "knowledge.refuted", Data: map[string]any{"knowledge_id": candidate.ID, "refutes": existing.ID}})
	}
	if len(candidate.Contradicts) > 0 {
		candidate.Contradicts = uniqueSorted(append(candidate.Contradicts, candidate.ID))
		candidate.Trust = KnowledgeContested
		events = append(events, KnowledgeEvent{Type: "knowledge.review_required", Data: map[string]any{"review_of": candidate.Contradicts}})
	}
	candidate.Contradicts = uniqueSorted(candidate.Contradicts)
	events = append(events, KnowledgeEvent{Type: "knowledge.promoted", Data: candidate})
	return candidate, events, nil
}

// ResolveReview accepts exactly one support and one refute for the bound pair.
func ResolveReview(reviewOf []string, proposal []KnowledgeProposal) (winnerID, loserID string, err error) {
	pair := uniqueSorted(reviewOf)
	if len(pair) != 2 {
		return "", "", errors.New("review requires exactly two knowledge ids")
	}
	for _, item := range proposal {
		if len(item.Supports) == 1 && len(item.Refutes) == 1 {
			support, refute := item.Supports[0], item.Refutes[0]
			if support != refute && containsString(pair, support) && containsString(pair, refute) {
				return support, refute, nil
			}
		}
	}
	return "", "", errors.New("review must support one bound id and refute the other")
}

func KnowledgeFingerprint(knowledge Knowledge) string {
	payload := strings.Join([]string{normalizeKnowledgeTopic(knowledge.Topic), string(knowledge.Scope), string(knowledge.Kind), knowledge.Target}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func FilterKnowledge(items []Knowledge, query KnowledgeQuery) []Knowledge {
	now := query.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	out := make([]Knowledge, 0, len(items))
	for _, item := range items {
		if item.TaskID != query.TaskID || item.Trust == KnowledgeRefuted {
			continue
		}
		if item.ExpiresAt != nil && !item.ExpiresAt.After(now) {
			continue
		}
		if !query.AllowCrossRun && query.RunID != "" && item.RunID != query.RunID {
			continue
		}
		if item.Reuse == ReuseNever && item.RunID != query.RunID {
			continue
		}
		if item.Reuse == ReuseInstance && item.Target != query.TargetFingerprint && item.Target != "" {
			continue
		}
		if (item.Scope == KnowledgeScopeTarget || item.Scope == KnowledgeScopeInstance) && item.Target != "" && item.Target != query.TargetFingerprint {
			continue
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := knowledgeTrustRank(out[i].Trust), knowledgeTrustRank(out[j].Trust)
		if ri != rj {
			return ri < rj
		}
		if !out[i].ValidFrom.Equal(out[j].ValidFrom) {
			return out[i].ValidFrom.After(out[j].ValidFrom)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func targetFingerprint(target Target) string {
	addresses := append([]string(nil), target.Addresses...)
	sort.Strings(addresses)
	payload := target.ID + "\x00" + strings.Join(addresses, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func knowledgeID(item Knowledge, version int64) string {
	sum := sha256.Sum256([]byte(KnowledgeFingerprint(item) + "\x00" + item.RunID + "\x00" + fmt.Sprint(version)))
	return "knowledge-" + hex.EncodeToString(sum[:])[:24]
}

func findKnowledge(items []Knowledge, id string) *Knowledge {
	for i := range items {
		if items[i].ID == id {
			return &items[i]
		}
	}
	return nil
}
func distinctSessions(sources []KnowledgeSource) int {
	seen := map[string]struct{}{}
	for _, source := range sources {
		if source.SessionID != "" {
			seen[source.SessionID] = struct{}{}
		}
	}
	return len(seen)
}
func appendUniqueSource(values []KnowledgeSource, additions ...KnowledgeSource) []KnowledgeSource {
	seen := map[string]struct{}{}
	out := make([]KnowledgeSource, 0, len(values)+len(additions))
	for _, value := range append(append([]KnowledgeSource(nil), values...), additions...) {
		key := value.RunID + "\x00" + value.AttemptID + "\x00" + value.SessionID + "\x00" + value.Observation
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}
func uniqueSorted(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
