package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type defaultExploreStrategy struct{}

func (defaultExploreStrategy) ExecutePrompt(req ExploreRequest) (string, error) {
	var out strings.Builder
	out.WriteString("You are an Explore task for one bounded authorized exploration Intent.\n")
	fmt.Fprintf(&out, "Intent: id=%s description=%s\n", req.Intent.ID, req.Intent.Description)
	fmt.Fprintf(&out, "Objective: %s\nTarget: id=%s addresses=%s\n", req.Task.Objective, req.Target.TargetID, strings.Join(req.Target.Addresses, ","))
	fmt.Fprintf(&out, "Budget: %d tool calls, %s\n", req.Budget.MaxToolCalls, req.Budget.MaxWallTime)
	out.WriteString("Source facts:\n")
	for _, fact := range req.Facts {
		fmt.Fprintf(&out, "- id=%s kind=%s description=%s\n", fact.ID, fact.Kind, truncateUTF8(fact.Description, 1024))
	}
	out.WriteString("Hints:\n")
	for _, hint := range req.Hints {
		fmt.Fprintf(&out, "- %s\n", truncateUTF8(hint.Content, 512))
	}
	out.WriteString(`Return exactly one JSON object and nothing else.
Example finding: {"intent_id":"INTENT_ID","phase":"concluded","fact":{"kind":"finding","description":"confirmed result","observation_refs":["obs-1"]},"observations":[{"local_id":"obs-1","kind":"adapter.namespace","summary":"observed result","target":"TARGET_ID","evidence_files":["relative/file"]}]}
Example negative: {"intent_id":"INTENT_ID","phase":"concluded","fact":{"kind":"negative","description":"what was ruled out"}}
Only produce a finding or negative Fact. Do not create Intents, child nodes, Knowledge, or a success verdict. A finding requires evidence files.`)
	return out.String(), nil
}

func (defaultExploreStrategy) ConcludePrompt(req ExploreRequest) (string, error) {
	return fmt.Sprintf(`Stop exploring immediately. Do not run another tool, scan, wait, or start any new action.
Summarize only what was already confirmed for Intent %s.
Return exactly one JSON object using the same schema as the execute phase. If nothing was confirmed, return a negative fact.
Do not create Intents, child nodes, Knowledge, or a success verdict.`, req.Intent.ID), nil
}

func (defaultExploreStrategy) Parse(req ExploreRequest, turn TurnResult) (ExploreReport, error) {
	trimmed := stripCodeFence(turn.AssistantText)
	if strings.TrimSpace(trimmed) == "" {
		return ExploreReport{}, fmt.Errorf("explore returned no JSON")
	}
	var report ExploreReport
	if err := json.Unmarshal([]byte(extractJSONObject(trimmed)), &report); err != nil {
		return ExploreReport{}, fmt.Errorf("parse explore report: %w", err)
	}
	if report.IntentID == "" || strings.EqualFold(report.IntentID, "INTENT_ID") {
		report.IntentID = req.Intent.ID
	}
	if report.IntentID != req.Intent.ID {
		return ExploreReport{}, fmt.Errorf("explore intent_id %q does not match %q", report.IntentID, req.Intent.ID)
	}
	if report.Phase == "" {
		report.Phase = IntentAttemptConcluded
	}
	if err := ValidateExploreReport(report); err != nil {
		return ExploreReport{}, err
	}
	return report, nil
}

// runExploreAttempt executes one Intent and invokes the same-session Conclude
// fallback when execution times out or produces unusable output. The returned
// phase is "execute" or "conclude".
func runExploreAttempt(ctx context.Context, session AgentSession, req ExploreRequest, limits TurnLimits, concludeTimeout time.Duration, sink EventSink, strategy ExploreStrategy) (ExploreReport, string, error) {
	if strategy == nil {
		strategy = defaultExploreStrategy{}
	}
	if session == nil {
		return ExploreReport{}, "", errors.New("agent session is required")
	}
	executePrompt, err := strategy.ExecutePrompt(req)
	if err != nil {
		return ExploreReport{}, "", err
	}
	turn, executeErr := runAgentTurn(ctx, session, executePrompt, limits, sink, false)
	var parseErr error
	if executeErr == nil {
		report, err := strategy.Parse(req, turn)
		if err == nil {
			return report, "execute", nil
		}
		parseErr = err
	}
	if !shouldConclude(executeErr, parseErr) {
		if executeErr != nil {
			return ExploreReport{}, "execute", executeErr
		}
		return ExploreReport{}, "execute", parseErr
	}
	if concludeTimeout <= 0 {
		concludeTimeout = DefaultRunPolicy().Exploration.ConcludeTimeout
	}
	concludeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), concludeTimeout)
	defer cancel()
	// Abort the active execute turn without killing the session, then ask the
	// same session to summarize what was already confirmed.
	if err := session.Abort(concludeCtx); err != nil {
		_ = abortSession(session)
		return ExploreReport{}, "conclude", errors.Join(executeErr, parseErr, fmt.Errorf("abort execute turn: %w", err))
	}
	concludePrompt, err := strategy.ConcludePrompt(req)
	if err != nil {
		_ = abortSession(session)
		return ExploreReport{}, "conclude", err
	}
	concludeTurn, concludeErr := runAgentTurn(concludeCtx, session, concludePrompt, TurnLimits{MaxAssistantBytes: limits.MaxAssistantBytes}, sink, true)
	if concludeErr != nil {
		_ = abortSession(session)
		return ExploreReport{}, "conclude", errors.Join(executeErr, parseErr, concludeErr)
	}
	report, err := strategy.Parse(req, concludeTurn)
	if err != nil {
		_ = abortSession(session)
		return ExploreReport{}, "conclude", err
	}
	return report, "conclude", nil
}

func shouldConclude(executeErr, parseErr error) bool {
	if parseErr != nil {
		return true
	}
	if executeErr == nil {
		return false
	}
	if errors.Is(executeErr, ErrAgentUnavailable) || errors.Is(executeErr, context.Canceled) {
		return false
	}
	if errors.Is(executeErr, context.DeadlineExceeded) {
		return true
	}
	message := strings.ToLower(executeErr.Error())
	return strings.Contains(message, "tool timeout") || strings.Contains(message, "tool call limit")
}
