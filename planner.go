package harness

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
)

const MaxPlannedItems = 3

// PlanRequest is the bounded blackboard projection visible to the Planner.
type PlanRequest struct {
	Round       int
	Objective   string
	Targets     []Target
	Progress    Completion
	Facts       []Fact
	OpenItems   []FrontierItem
	RecentItems []FrontierItem
	Reports     []ExplorerReport
}

// PlanOutput contains candidate frontiers only. The Controller owns acceptance,
// deduplication, assignment and execution.
type PlanOutput struct {
	NextItems []ExplorerProposal `json:"next_items"`
}

// Planner performs semantic planning from a bounded state snapshot.
type Planner interface {
	Plan(ctx context.Context, req PlanRequest) (PlanOutput, error)
}

// DroppedItem records a planner proposal rejected by deterministic validation.
type DroppedItem struct {
	Proposal ExplorerProposal
	Reason   string
}

// PlanValidation is the fail-closed result of proposal validation.
type PlanValidation struct {
	Accepted []FrontierItem
	Dropped  []DroppedItem
}

func ParsePlanOutput(data []byte) (PlanOutput, error) {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "```") {
		if newline := strings.IndexByte(trimmed, '\n'); newline >= 0 {
			trimmed = trimmed[newline+1:]
		}
		trimmed = strings.TrimSuffix(strings.TrimSpace(trimmed), "```")
	}
	var output PlanOutput
	if err := json.Unmarshal([]byte(trimmed), &output); err != nil {
		return PlanOutput{}, fmt.Errorf("parse plan output: %w", err)
	}
	return output, nil
}

// ValidatePlan is the deterministic deduplication and authorization boundary.
func ValidatePlan(output PlanOutput, ledger *FactLedger, existing []FrontierItem, targets []Target) (PlanValidation, error) {
	validation := PlanValidation{}
	seen := map[string]struct{}{}
	for _, item := range existing {
		fingerprint := item.Fingerprint
		if fingerprint == "" {
			fingerprint = FrontierFingerprint(item)
		}
		if fingerprint != "" {
			seen[fingerprint] = struct{}{}
		}
	}
	for _, proposal := range output.NextItems {
		proposal.Question = strings.TrimSpace(proposal.Question)
		proposal.Target = strings.TrimSpace(proposal.Target)
		proposal.ExpectedEvidence = strings.TrimSpace(proposal.ExpectedEvidence)
		if proposal.Question == "" {
			validation.Dropped = append(validation.Dropped, DroppedItem{Proposal: proposal, Reason: "empty question"})
			continue
		}
		if proposal.Target == "" {
			validation.Dropped = append(validation.Dropped, DroppedItem{Proposal: proposal, Reason: "empty target"})
			continue
		}
		if !AuthorizeTarget(proposal.Target, targets) {
			validation.Dropped = append(validation.Dropped, DroppedItem{Proposal: proposal, Reason: "unauthorized target"})
			continue
		}
		if proposal.ExpectedEvidence == "" {
			validation.Dropped = append(validation.Dropped, DroppedItem{Proposal: proposal, Reason: "empty expected evidence"})
			continue
		}
		item := FrontierItem{
			Question:         proposal.Question,
			TargetHint:       proposal.Target,
			ExpectedEvidence: proposal.ExpectedEvidence,
			Status:           FrontierOpen,
			Anchors:          corroboratedAnchors(ledger, proposal.Anchors),
		}
		item.Fingerprint = FrontierFingerprint(item)
		if _, duplicate := seen[item.Fingerprint]; duplicate {
			validation.Dropped = append(validation.Dropped, DroppedItem{Proposal: proposal, Reason: "duplicate frontier"})
			continue
		}
		item.ID = "frontier-" + item.Fingerprint[:16]
		seen[item.Fingerprint] = struct{}{}
		validation.Accepted = append(validation.Accepted, item)
		if len(validation.Accepted) == MaxPlannedItems {
			break
		}
	}
	return validation, nil
}

// FrontierFingerprint is the exact-action identity used for deduplication.
// It intentionally ignores free-form priority and prose outside the assigned
// hypothesis.
func FrontierFingerprint(item FrontierItem) string {
	payload := strings.Join([]string{
		normalizeFrontierText(item.TargetHint),
		normalizeFrontierText(item.Question),
		normalizeFrontierText(item.ExpectedEvidence),
	}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("%x", sum[:])
}

// SeedItems provides deterministic bootstrap work when planning fails.
func SeedItems(targets []Target, objective string, round int) []FrontierItem {
	items := make([]FrontierItem, 0, len(targets))
	for index, target := range targets {
		hint := strings.TrimSpace(target.ID)
		if hint == "" && len(target.Addresses) > 0 {
			hint = strings.TrimSpace(target.Addresses[0])
		}
		if hint == "" {
			continue
		}
		question := "Map the reachable attack surface and concrete entry points for " + hint
		if objective != "" {
			question += " in service of: " + objective
		}
		item := FrontierItem{
			ID:               fmt.Sprintf("seed-%d-%d", round, index+1),
			Question:         question,
			TargetHint:       hint,
			ExpectedEvidence: "a structured list of reachable services, paths, and candidate entry points",
			Status:           FrontierOpen,
			Round:            round,
		}
		item.Fingerprint = FrontierFingerprint(item)
		items = append(items, item)
	}
	return items
}

func corroboratedAnchors(ledger *FactLedger, anchors []string) []string {
	if ledger == nil {
		return nil
	}
	out := make([]string, 0, len(anchors))
	for _, id := range anchors {
		fact, ok := ledger.Get(id)
		if ok && fact.Trust == FactCorroborated {
			out = appendUnique(out, id)
		}
	}
	return out
}

func normalizeFrontierText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}
