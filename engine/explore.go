package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type defaultExploreStrategy struct{}

// DefaultExploreStrategy returns the protocol-compliant Explore strategy.
func DefaultExploreStrategy() ExploreStrategy { return defaultExploreStrategy{} }

// RunExploreAttempt is the exported bounded Execute/Conclude turn pair used by
// the v5 kernel. It shares the v4 protocol implementation.
func RunExploreAttempt(ctx context.Context, session AgentSession, req ExploreRequest, limits TurnLimits, concludeTimeout time.Duration, sink EventSink, strategy ExploreStrategy, clock Clock, workspace Workspace) (AttemptReport, string, error) {
	return runExploreAttempt(ctx, session, req, limits, concludeTimeout, sink, strategy, clock, workspace)
}

func (defaultExploreStrategy) ExecutePrompt(req ExploreRequest) (string, error) {
	var out strings.Builder
	out.WriteString("You are an Explore task for one bounded authorized exploration Intent.\n")
	fmt.Fprintf(&out, "Intent: id=%s description=%s\n", req.Intent.ID, req.Intent.Description)
	fmt.Fprintf(&out, "Objective: %s\nTarget: id=%s addresses=%s\n", req.Task.Objective, req.Target.TargetID, strings.Join(req.Target.Addresses, ","))
	fmt.Fprintf(&out, "Budget: %d tool calls, %s\n", req.Budget.MaxToolCalls, req.Budget.MaxWallTime)
	out.WriteString("Source claims:\n")
	for _, claim := range req.Claims {
		fmt.Fprintf(&out, "- id=%s kind=%s description=%s\n", claim.ID, claim.Kind, truncateUTF8(claim.Description, 1024))
	}
	out.WriteString(reportProtocolInstruction)
	out.WriteString(`Return exactly one JSON object and nothing else.
Example finding: {"intent_id":"INTENT_ID","phase":"concluded","claim":{"kind":"finding","description":"confirmed result","observation_refs":["obs-1"]},"observations":[{"local_id":"obs-1","kind":"adapter.namespace","summary":"observed result","target":"TARGET_ID","evidence_files":["relative/file"]}]}
Example negative: {"intent_id":"INTENT_ID","phase":"concluded","claim":{"kind":"negative","description":"what was ruled out","observation_refs":["obs-1"]},"observations":[{"local_id":"obs-1","kind":"adapter.namespace","summary":"what was ruled out","target":"TARGET_ID","evidence_files":["relative/file"]}]}
Only produce a finding or negative Claim. Do not create Intents, child nodes, Knowledge, or a success verdict. Every finding and negative requires evidence files.`)
	return out.String(), nil
}

func (defaultExploreStrategy) ConcludePrompt(req ExploreRequest) (string, error) {
	return fmt.Sprintf(`Stop exploring immediately. Do not run another investigative tool, scan, wait, or start any new action. Only use the file-writing tool to write the final report.
Summarize only what was already confirmed for Intent %s.
Return exactly one JSON object using the same schema as the execute phase. If nothing was confirmed, return a negative claim with at least one evidence file.
Do not create Intents, child nodes, Knowledge, or a success verdict.
%s`, req.Intent.ID, reportProtocolInstruction), nil
}

func (defaultExploreStrategy) Parse(req ExploreRequest, turn TurnResult) (AttemptReport, error) {
	var report AttemptReport
	if err := decodeReport(turn.Report, &report); err != nil {
		return AttemptReport{}, fmt.Errorf("parse attempt report: %w", err)
	}
	if report.IntentID == "" || strings.EqualFold(report.IntentID, "INTENT_ID") {
		report.IntentID = req.Intent.ID
	}
	if report.IntentID != req.Intent.ID {
		return AttemptReport{}, fmt.Errorf("explore intent_id %q does not match %q", report.IntentID, req.Intent.ID)
	}
	if report.Phase == "" {
		report.Phase = IntentAttemptConcluded
	}
	if err := ValidateAttemptReport(report); err != nil {
		return AttemptReport{}, err
	}
	return report, nil
}

// runExploreAttempt executes one Intent and invokes the same-session Conclude
// fallback when execution times out or produces unusable output. The returned
// phase is "execute" or "conclude".
func runExploreAttempt(ctx context.Context, session AgentSession, req ExploreRequest, limits TurnLimits, concludeTimeout time.Duration, sink EventSink, strategy ExploreStrategy, clock Clock, workspace Workspace) (AttemptReport, string, error) {
	if strategy == nil {
		strategy = defaultExploreStrategy{}
	}
	if session == nil {
		return AttemptReport{}, "", errors.New("agent session is required")
	}
	executePrompt, err := strategy.ExecutePrompt(req)
	if err != nil {
		return AttemptReport{}, "", err
	}
	if req.WorkspaceDir != "" {
		if err := workspace.ClearReport(ctx, req.WorkspaceDir); err != nil {
			return AttemptReport{}, "execute", err
		}
	}
	turn, executeErr := runAgentTurn(ctx, session, executePrompt, limits, sink, false, clock)
	var parseErr error
	if executeErr == nil && req.WorkspaceDir != "" {
		turn.Report, executeErr = workspace.ReadReport(ctx, req.WorkspaceDir, req.MaxReportBytes)
	}
	if executeErr == nil {
		report, err := strategy.Parse(req, turn)
		if err == nil {
			return report, "execute", nil
		}
		parseErr = err
	}
	if !shouldConclude(executeErr, parseErr) {
		if executeErr != nil {
			return AttemptReport{}, "execute", executeErr
		}
		return AttemptReport{}, "execute", parseErr
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
		return AttemptReport{}, "conclude", errors.Join(executeErr, parseErr, fmt.Errorf("abort execute turn: %w", err))
	}
	drainAbortedTurn(session, concludeCtx)
	concludePrompt, err := strategy.ConcludePrompt(req)
	if err != nil {
		_ = abortSession(session)
		return AttemptReport{}, "conclude", err
	}
	if req.WorkspaceDir != "" {
		if err := workspace.ClearReport(concludeCtx, req.WorkspaceDir); err != nil {
			_ = abortSession(session)
			return AttemptReport{}, "conclude", err
		}
	}
	concludeTurn, concludeErr := runAgentTurn(concludeCtx, session, concludePrompt, TurnLimits{
		MaxAssistantBytes: limits.MaxAssistantBytes,
		ReportOnly:        true,
	}, sink, true, clock)
	if concludeErr == nil && req.WorkspaceDir != "" {
		concludeTurn.Report, concludeErr = workspace.ReadReport(concludeCtx, req.WorkspaceDir, req.MaxReportBytes)
	}
	if concludeErr != nil {
		_ = abortSession(session)
		return AttemptReport{}, "conclude", errors.Join(executeErr, parseErr, concludeErr)
	}
	report, err := strategy.Parse(req, concludeTurn)
	if err != nil {
		_ = abortSession(session)
		return AttemptReport{}, "conclude", err
	}
	return report, "conclude", nil
}

// drainAbortedTurn discards the settlement emitted for the turn that was just
// aborted. Otherwise that stale agent_settled can be mistaken for completion of
// the subsequent Conclude turn before the agent has written its report.
func drainAbortedTurn(session AgentSession, ctx context.Context) {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-session.Events():
			if !ok || event.Kind == AgentEventSettled {
				return
			}
		case <-timer.C:
			return
		case <-ctx.Done():
			return
		}
	}
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
