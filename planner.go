package harness

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// buildPlannerContext creates the deterministic, bounded planner view.
func buildPlannerContext(task Task, target Target, branches []Branch, knowledge []Knowledge, progress Completion, attempts []Attempt, policy ExplorationPolicy) (PlanRequest, int) {
	policy = NormalizeExplorationPolicy(policy)
	summaries := make([]BranchSummary, 0, len(branches))
	for i, branch := range branches {
		if i >= policy.MaxNodes {
			break
		}
		summaries = append(summaries, BranchSummary{
			ID: branch.ID, ParentID: branch.ParentID, Kind: branch.Kind, Depth: branch.Depth,
			Topic: truncateUTF8(branch.Topic, 256), Hypothesis: truncateUTF8(branch.Hypothesis, 512),
			Scope: truncateUTF8(branch.Scope, 256), Status: branch.Status, Priority: branch.Priority,
			ReviewOf: append([]string(nil), branch.ReviewOf...),
		})
	}
	digest, dropped := boundedKnowledgeDigest(knowledge, target, policy, time.Now().UTC())
	start := len(attempts) - policy.MaxActiveBranches
	if start < 0 {
		start = 0
	}
	recent := make([]AttemptSummary, 0, len(attempts)-start)
	for _, attempt := range attempts[start:] {
		recent = append(recent, AttemptSummary{ID: attempt.ID, BranchID: attempt.BranchID, Status: attempt.Status, Outcome: attempt.Outcome, Error: truncateUTF8(attempt.Error, 512)})
	}
	return PlanRequest{
		Task: task, Target: target, Branches: summaries, Knowledge: digest,
		Progress: progress, LastReports: recent,
	}, dropped
}

// buildExecutorContext creates the deterministic, bounded executor view.
func buildExecutorContext(task Task, target Target, branch Branch, knowledge []Knowledge, budget AttemptBudget, workspaceDir string, policy ExplorationPolicy) (AttemptRequest, int) {
	policy = NormalizeExplorationPolicy(policy)
	digest, dropped := boundedKnowledgeDigest(knowledge, target, policy, time.Now().UTC())
	return AttemptRequest{
		Task: task, Target: target, Branch: branch, Knowledge: digest,
		Budget: budget, WorkspaceDir: workspaceDir, MaxReportBytes: policy.MaxReportBytes,
	}, dropped
}

func boundedKnowledgeDigest(items []Knowledge, target Target, policy ExplorationPolicy, now time.Time) ([]Knowledge, int) {
	filtered := make([]Knowledge, 0, len(items))
	for _, item := range items {
		if item.Trust == KnowledgeRefuted || item.Trust == "" {
			continue
		}
		if item.ExpiresAt != nil && !item.ExpiresAt.After(now) {
			continue
		}
		if (item.Scope == KnowledgeScopeTarget || item.Scope == KnowledgeScopeInstance) && item.Target != "" && item.Target != target.ID && item.Target != targetFingerprint(target) {
			continue
		}
		filtered = append(filtered, item)
	}
	sortKnowledgeByTrust(filtered)
	limit := policy.MaxKnowledgeDigestItems
	if limit <= 0 || limit > len(filtered) {
		limit = len(filtered)
	}
	out := make([]Knowledge, 0, limit)
	total := 0
	for _, item := range filtered[:limit] {
		statement := truncateUTF8(item.Statement, policy.MaxKnowledgeStatementBytes)
		if total+len(statement) > policy.MaxKnowledgeDigestBytes {
			break
		}
		total += len(statement)
		item.Statement = statement
		out = append(out, item)
	}
	return out, len(items) - len(out)
}

func knowledgeTrustRank(trust KnowledgeTrust) int {
	switch trust {
	case KnowledgeCorroborated:
		return 0
	case KnowledgeObserved:
		return 1
	case KnowledgeContested:
		return 2
	default:
		return 3
	}
}

func truncateUTF8(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	value = value[:max]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

type defaultPlannerStrategy struct{}

func (defaultPlannerStrategy) Prompt(req PlanRequest) (string, error) { return plannerPrompt(req), nil }
func (defaultPlannerStrategy) Parse(req PlanRequest, turn TurnResult) (PlanProposal, error) {
	trimmed := stripCodeFence(turn.AssistantText)
	if strings.TrimSpace(trimmed) == "" {
		return PlanProposal{}, fmt.Errorf("planner returned no JSON")
	}
	var proposal PlanProposal
	if err := json.Unmarshal([]byte(trimmed), &proposal); err != nil {
		return PlanProposal{}, fmt.Errorf("parse planner proposal: %w", err)
	}
	return proposal, nil
}

func plannerPrompt(req PlanRequest) string {
	var out strings.Builder
	out.WriteString("You are the root planner for an authorized security task. Use read-only tools only.\n")
	fmt.Fprintf(&out, "Objective: %s\nTarget: id=%s addresses=%s isolation=%s\n", req.Task.Objective, req.Target.ID, strings.Join(req.Target.Addresses, ","), req.Target.Isolation)
	if req.Progress.Total > 0 {
		fmt.Fprintf(&out, "Authoritative progress: %d/%d\n", req.Progress.Progress, req.Progress.Total)
	}
	out.WriteString("Branches:\n")
	for _, branch := range req.Branches {
		fmt.Fprintf(&out, "- id=%s parent=%s depth=%d topic=%s status=%s hypothesis=%s\n", branch.ID, branch.ParentID, branch.Depth, branch.Topic, branch.Status, branch.Hypothesis)
	}
	out.WriteString("Knowledge:\n")
	for _, item := range req.Knowledge {
		fmt.Fprintf(&out, "- [%s] %s: %s\n", item.Trust, item.Topic, item.Statement)
	}
	out.WriteString("Recent attempts:\n")
	for _, attempt := range req.LastReports {
		fmt.Fprintf(&out, "- branch=%s status=%s outcome=%s\n", attempt.BranchID, attempt.Status, attempt.Outcome)
	}
	out.WriteString(`Return exactly one JSON object: {"create_branches":[{"parent_id":"root","topic":"semantic.topic","goal":"...","hypothesis":"...","success_condition":"...","failure_condition":"...","target":"TARGET_ID","priority":0}],"close_branches":[],"priorities":{}}.
Create only root-level branches. The engine enforces node/depth/duplicate limits. Do not claim success.`)
	return out.String()
}

func stripCodeFence(value string) string {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "```") {
		if newline := strings.IndexByte(trimmed, '\n'); newline >= 0 {
			trimmed = trimmed[newline+1:]
		}
		trimmed = strings.TrimSuffix(strings.TrimSpace(trimmed), "```")
	}
	return strings.TrimSpace(trimmed)
}
