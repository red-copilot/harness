package harness

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
	"unicode"
)

const MaxPlannedItems = 3

// PlanRequest is the bounded blackboard projection visible to the Planner.
type PlanRequest struct {
	Round       int
	Objective   string
	Metadata    map[string]string
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
		proposal.ProbeKey = strings.TrimSpace(proposal.ProbeKey)
		proposal.ExpectedEvidence = strings.TrimSpace(proposal.ExpectedEvidence)
		proposal.FailureEvidence = strings.TrimSpace(proposal.FailureEvidence)
		proposal.Params = normalizeProbeParams(proposal.Params)
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
		if proposal.FailureEvidence == "" {
			validation.Dropped = append(validation.Dropped, DroppedItem{Proposal: proposal, Reason: "empty failure_evidence"})
			continue
		}
		if reason := validateProbeContract(proposal.ProbeKey, proposal.Params); reason != "" {
			validation.Dropped = append(validation.Dropped, DroppedItem{Proposal: proposal, Reason: reason})
			continue
		}
		target, ok := canonicalAuthorizedTarget(proposal.Target, targets)
		if !ok {
			validation.Dropped = append(validation.Dropped, DroppedItem{Proposal: proposal, Reason: "target is not one exact authorized id/address"})
			continue
		}
		item := FrontierItem{
			Question:         proposal.Question,
			TargetHint:       target,
			ProbeKey:         proposal.ProbeKey,
			Params:           append([]string(nil), proposal.Params...),
			ExpectedEvidence: proposal.ExpectedEvidence,
			FailureEvidence:  proposal.FailureEvidence,
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

// FrontierFingerprint is the exact experiment identity used for deduplication.
// Rephrasing a hypothesis does not create a new experiment.
func FrontierFingerprint(item FrontierItem) string {
	parts := []string{normalizeFrontierText(item.TargetHint)}
	if item.ProbeKey == "" {
		parts = append(parts,
			normalizeFrontierText(item.Question),
			normalizeFrontierText(item.ExpectedEvidence),
		)
	} else {
		parts = append(parts, normalizeFrontierText(item.ProbeKey))
		for _, param := range item.Params {
			parts = append(parts, normalizeFrontierText(param))
		}
	}
	payload := strings.Join(parts, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("%x", sum[:])
}

// SeedItems provides deterministic bootstrap work when planning fails.
func SeedItems(targets []Target, objective string, brief map[string]string, round int) []FrontierItem {
	items := make([]FrontierItem, 0, len(targets))
	for index, target := range targets {
		if len(items) == DefaultExplorationWorkers {
			break
		}
		hint := strings.TrimSpace(target.ID)
		if hint == "" && len(target.Addresses) > 0 {
			hint = strings.TrimSpace(target.Addresses[0])
		}
		if hint == "" {
			continue
		}
		actionTarget := hint
		if len(target.Addresses) > 0 && strings.TrimSpace(target.Addresses[0]) != "" {
			actionTarget = strings.TrimSpace(target.Addresses[0])
		}
		item := FrontierItem{
			ID:                 fmt.Sprintf("seed-%d-%d", round, index+1),
			Question:           seedQuestion(hint, objective, brief),
			TargetHint:         hint,
			ProbeKey:           "service.banner",
			Params:             []string{actionTarget},
			ExpectedEvidence:   "连接建立并返回任何应用层字节（协议横幅、登录提示或选项协商）",
			FailureEvidence:    "连接被拒绝、超时，或建立后返回零字节",
			MaxTools:           DefaultFrontierToolLimit,
			MaxDurationSeconds: DefaultFrontierDurationSeconds,
			Status:             FrontierOpen,
			Round:              round,
		}
		item.Fingerprint = FrontierFingerprint(item)
		items = append(items, item)
	}
	return items
}

// seedQuestion derives a deterministic first hypothesis from the task brief, so
// the blackboard is meaningful before the Planner's first model call.
func seedQuestion(hint, objective string, brief map[string]string) string {
	question := "授权目标 " + hint + " 的对外服务是否可达，并返回可识别的协议横幅或登录提示？"
	if summary := briefSummary(brief); summary != "" {
		question += "（题目：" + summary + "）"
	} else if objective != "" {
		question += "（目标：" + truncateBytes(objective, 160) + "）"
	}
	return question
}

func briefSummary(brief map[string]string) string {
	for _, key := range []string{"challenge_description", "description", "brief"} {
		if value := strings.TrimSpace(brief[key]); value != "" {
			return truncateBytes(value, 160)
		}
	}
	return ""
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

func normalizeProbeParams(params []string) []string {
	out := make([]string, 0, len(params))
	for _, param := range params {
		param = strings.TrimSpace(param)
		if param == "" {
			continue
		}
		out = append(out, param)
		if len(out) == 4 {
			break
		}
	}
	return out
}

func validateProbeContract(probeKey string, params []string) string {
	if probeKey == "" {
		return "empty probe_key"
	}
	if len(probeKey) < 3 || len(probeKey) > 64 {
		return "probe_key must be 3-64 characters"
	}
	for _, r := range probeKey {
		if unicode.IsLower(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' || r == ':' {
			continue
		}
		return "probe_key must use lowercase letters, digits, '.', '_', '-' or ':'"
	}
	for _, fragment := range []string{"scan", "enum", "brute", "fuzz", "sweep", "crawl", "discover", "attack_surface"} {
		if strings.Contains(probeKey, fragment) {
			return "open-ended probe is not allowed"
		}
	}
	if len(params) == 0 {
		return "probe params are required"
	}
	if len(params) > 4 {
		return "probe params exceed limit"
	}
	if (strings.HasSuffix(probeKey, ".login") || strings.HasSuffix(probeKey, ".password")) && len(params) < 3 {
		return "credential probe params require host, username and password"
	}
	for _, param := range params {
		if param == "" || len(param) > 256 {
			return "probe params must be 1-256 bytes"
		}
		if strings.ContainsAny(param, "\r\n\x00") {
			return "probe params contain control characters"
		}
		if !isLiteralProbeParam(param) {
			return "probe params must be single literal values"
		}
	}
	return ""
}

func isLiteralProbeParam(param string) bool {
	lower := strings.ToLower(param)
	if strings.Contains(lower, "fuzz") ||
		strings.Contains(lower, "wordlist") ||
		strings.ContainsAny(param, "*?[]{}$,") ||
		strings.HasPrefix(param, "-") {
		return false
	}
	if _, _, err := net.ParseCIDR(param); err == nil {
		return false
	}
	for index := 1; index+1 < len(param); index++ {
		if param[index] == '-' &&
			param[index-1] >= '0' && param[index-1] <= '9' &&
			param[index+1] >= '0' && param[index+1] <= '9' {
			return false
		}
	}
	return true
}

func canonicalAuthorizedTarget(target string, targets []Target) (string, bool) {
	if len(strings.Fields(target)) != 1 {
		return "", false
	}
	for _, authorized := range targets {
		if authorized.ID != "" && strings.EqualFold(target, authorized.ID) {
			return authorized.ID, true
		}
		for _, address := range authorized.Addresses {
			if strings.EqualFold(target, address) ||
				(scopeHost(target) != "" && strings.EqualFold(scopeHost(target), scopeHost(address))) {
				if authorized.ID != "" {
					return authorized.ID, true
				}
				return address, true
			}
		}
	}
	return "", false
}

func plannerFactDigest(facts []Fact, limit int) []Fact {
	type indexedFact struct {
		index int
		fact  Fact
	}
	filtered := make([]indexedFact, 0, len(facts))
	for index, fact := range facts {
		if fact.Trust == FactRefuted {
			continue
		}
		filtered = append(filtered, indexedFact{index: index, fact: copyFact(fact)})
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		left, right := filtered[i], filtered[j]
		leftRank, rightRank := trustRank(left.fact.Trust), trustRank(right.fact.Trust)
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		if left.fact.Contested != right.fact.Contested {
			return left.fact.Contested
		}
		return left.index > right.index
	})
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	out := make([]Fact, 0, len(filtered))
	for _, item := range filtered {
		out = append(out, item.fact)
	}
	return out
}
