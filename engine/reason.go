package engine

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	maxReasonFacts = 64
	maxReasonHints = 16
)

type defaultReasonStrategy struct{}

func (defaultReasonStrategy) Prompt(req ReasonRequest) (string, error) {
	return reasonPrompt(req), nil
}

func (defaultReasonStrategy) Parse(req ReasonRequest, turn TurnResult) (ReasonReport, error) {
	trimmed := stripCodeFence(turn.AssistantText)
	if strings.TrimSpace(trimmed) == "" {
		return ReasonReport{}, fmt.Errorf("reason returned no JSON")
	}
	var report ReasonReport
	if err := json.Unmarshal([]byte(extractJSONObject(trimmed)), &report); err != nil {
		return ReasonReport{}, fmt.Errorf("parse reason report: %w", err)
	}
	if report.FrontierVersion == 0 {
		report.FrontierVersion = req.FrontierVersion
	}
	if err := ValidateReasonReport(report, len(req.FreeSlots)); err != nil {
		return ReasonReport{}, err
	}
	return report, nil
}

func buildReasonContext(snapshot boardSnapshot, target TargetBinding, hints []Hint, freeSlots []int, recent []Intent, policy RunPolicy) ReasonRequest {
	policy = NormalizeRunPolicy(policy)
	if len(hints) == 0 {
		hints = snapshot.Hints
	}
	facts := boundedFactsWithPolicy(snapshot.Facts, policy)
	intents := make([]Intent, 0, 2)
	for _, intent := range snapshot.Intents {
		if intent.State == IntentOpen || intent.State == IntentClaimed {
			intents = append(intents, intent)
		}
	}
	if len(intents) > 2 {
		intents = intents[len(intents)-2:]
	}
	hints = boundedHints(hints, policy)
	return ReasonRequest{
		Task:            snapshot.Task,
		Target:          target,
		Facts:           facts,
		Intents:         intents,
		Hints:           append([]Hint(nil), hints...),
		FreeSlots:       append([]int(nil), freeSlots...),
		FrontierVersion: snapshot.FrontierVersion,
		RecentFacts:     recentFacts(facts, 8),
		Policy:          policy,
	}
}

func boundedFacts(facts []Fact, limit int) []Fact {
	if limit <= 0 || len(facts) <= limit {
		return cloneFacts(facts)
	}
	out := make([]Fact, 0, limit)
	for _, fact := range facts {
		if fact.Kind == FactOrigin || fact.Kind == FactGoal {
			out = append(out, fact)
		}
	}
	for _, fact := range facts {
		if len(out) >= limit {
			break
		}
		if fact.Kind == FactOrigin || fact.Kind == FactGoal {
			continue
		}
		out = append(out, fact)
	}
	return cloneFacts(out)
}

func boundedFactsWithPolicy(facts []Fact, policy RunPolicy) []Fact {
	limit := policy.Context.MaxBoardItems
	if limit <= 0 || limit > maxReasonFacts {
		limit = maxReasonFacts
	}
	base := boundedFacts(facts, limit)
	out := make([]Fact, 0, len(base))
	var used int64
	for _, fact := range base {
		fact.Description = truncateUTF8(fact.Description, policy.Context.MaxFactBytes)
		cost := int64(len(fact.Description) + len(fact.ID) + 32)
		if len(out) >= 2 && policy.Context.MaxBoardBytes > 0 && used+cost > policy.Context.MaxBoardBytes {
			continue
		}
		used += cost
		out = append(out, fact)
	}
	return out
}

func boundedHints(hints []Hint, policy RunPolicy) []Hint {
	if len(hints) > maxReasonHints {
		hints = hints[len(hints)-maxReasonHints:]
	}
	out := make([]Hint, 0, len(hints))
	for _, hint := range hints {
		hint.Content = truncateUTF8(hint.Content, policy.Context.MaxHintBytes)
		out = append(out, hint)
	}
	return out
}

func recentFacts(facts []Fact, limit int) []Fact {
	if limit <= 0 || len(facts) <= limit {
		return cloneFacts(facts)
	}
	return cloneFacts(facts[len(facts)-limit:])
}

func reasonPrompt(req ReasonRequest) string {
	var out strings.Builder
	out.WriteString("You are the Reason task for an authorized security run.\n")
	out.WriteString("Read the Fact/Intent graph and choose the next exploration Intents.\n")
	fmt.Fprintf(&out, "Objective: %s\nTarget: id=%s addresses=%s\n", req.Task.Objective, req.Target.TargetID, strings.Join(req.Target.Addresses, ","))
	fmt.Fprintf(&out, "Frontier version: %d\n", req.FrontierVersion)
	fmt.Fprintf(&out, "Free slots: %s\n", formatSlotList(req.FreeSlots))
	fmt.Fprintf(&out, "At most %d new Intent(s) may be created.\n", len(req.FreeSlots))
	if req.Progress.Total > 0 {
		fmt.Fprintf(&out, "Authoritative progress: %d/%d\n", req.Progress.Progress, req.Progress.Total)
	}
	out.WriteString("Facts:\n")
	for _, fact := range req.Facts {
		fmt.Fprintf(&out, "- id=%s kind=%s description=%s\n", fact.ID, fact.Kind, truncateUTF8(fact.Description, 1024))
	}
	out.WriteString("Open or claimed Intents:\n")
	for _, intent := range req.Intents {
		fmt.Fprintf(&out, "- id=%s state=%s slot=%d description=%s\n", intent.ID, intent.State, intent.Slot, truncateUTF8(intent.Description, 512))
	}
	out.WriteString("Hints:\n")
	for _, hint := range req.Hints {
		fmt.Fprintf(&out, "- %s\n", truncateUTF8(hint.Content, 512))
	}
	out.WriteString(`Return exactly one JSON object and nothing else. Do not mark the Run successful; only the Verifier or backend completion may do that. A goal_candidate only requests verification.
Example: {"frontier_version":1,"intents":[{"from":["origin"],"description":"one bounded exploration direction"}]}
If the facts appear sufficient, return {"frontier_version":1,"goal_candidate":{"from":["fact-id"],"description":"why the evidence may satisfy the goal"}}.
Do not create more Intents than free slots, do not create child nodes, and do not repeat an existing open Intent.`)
	return out.String()
}

func formatSlotList(slots []int) string {
	if len(slots) == 0 {
		return "none"
	}
	parts := make([]string, len(slots))
	for i, slot := range slots {
		parts[i] = fmt.Sprintf("%d", slot)
	}
	return strings.Join(parts, ", ")
}
