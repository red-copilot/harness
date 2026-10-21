package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EventSink persists raw session events without letting the explorer consume
// the only copy of the audit stream.
type EventSink func(AgentEvent)

// TurnOutcome is the bounded result of one prompt/agent_settled cycle.
type TurnOutcome struct {
	AssistantText string
	ToolCalls     []string
	Truncated     bool
}

// RunTurn sends one prompt and waits for agent_settled, collecting only the
// assistant text and tool-call names needed by the role adapter.
func RunTurn(ctx context.Context, session AgentSession, prompt string, sink EventSink) (TurnOutcome, error) {
	if session == nil {
		return TurnOutcome{}, errors.New("agent session is required")
	}
	if err := session.Prompt(ctx, prompt); err != nil {
		return TurnOutcome{}, err
	}
	var outcome TurnOutcome
	for {
		select {
		case <-ctx.Done():
			return TurnOutcome{}, ctx.Err()
		case event, ok := <-session.Events():
			if !ok {
				if outcome.AssistantText == "" {
					return TurnOutcome{}, errors.New("agent event stream closed before settlement")
				}
				return TurnOutcome{}, errors.New("agent event stream closed before agent_settled")
			}
			if sink != nil {
				sink(event)
			}
			switch event.Type {
			case "turn_end":
				text, tools := parseTurnEvent(event.Data)
				if text != "" {
					outcome.AssistantText = text
				}
				outcome.ToolCalls = append(outcome.ToolCalls, tools...)
			case "agent_settled":
				return outcome, nil
			}
		}
	}
}

// SessionPlanner adapts an AgentSession to the Planner contract. It receives a
// bounded blackboard snapshot and returns JSON only.
type SessionPlanner struct {
	Session AgentSession
	Sink    EventSink
}

func (p *SessionPlanner) Plan(ctx context.Context, request PlanRequest) (PlanOutput, error) {
	if p == nil || p.Session == nil {
		return PlanOutput{}, errors.New("planner session is required")
	}
	resettable, ok := p.Session.(ResettableSession)
	if !ok {
		return PlanOutput{}, errors.New("planner session does not support fresh context reset")
	}
	if err := resettable.Reset(ctx); err != nil {
		return PlanOutput{}, fmt.Errorf("reset planner session: %w", err)
	}
	outcome, err := RunTurn(ctx, p.Session, buildPlanPrompt(request), p.Sink)
	if err != nil {
		return PlanOutput{}, err
	}
	if strings.TrimSpace(outcome.AssistantText) == "" {
		return PlanOutput{}, errors.New("planner returned no assistant JSON")
	}
	return ParsePlanOutput([]byte(outcome.AssistantText))
}

// SessionExplorer adapts an AgentSession to exactly one frontier execution.
type SessionExplorer struct {
	Session      AgentSession
	IDValue      string
	WorkspaceDir string
	ToolLimit    int
	Sink         EventSink
}

func (e *SessionExplorer) ID() string {
	if e.IDValue != "" {
		return e.IDValue
	}
	return "executor"
}

func (e *SessionExplorer) Reset(ctx context.Context) error {
	if e == nil || e.Session == nil {
		return errors.New("executor session is required")
	}
	resettable, ok := e.Session.(ResettableSession)
	if !ok {
		return errors.New("executor session does not support fresh context reset")
	}
	return resettable.Reset(ctx)
}

func (e *SessionExplorer) Investigate(ctx context.Context, item FrontierItem, brief string) (ExplorerReport, error) {
	if e == nil || e.Session == nil {
		return ExplorerReport{}, errors.New("executor session is required")
	}
	if e.WorkspaceDir == "" {
		return ExplorerReport{}, errors.New("executor workspace is required")
	}
	reportPath := filepath.Join(e.WorkspaceDir, ReportFileName)
	if err := os.Remove(reportPath); err != nil && !os.IsNotExist(err) {
		return ExplorerReport{}, fmt.Errorf("remove stale explorer report: %w", err)
	}
	outcome, err := RunTurn(ctx, e.Session, brief, e.Sink)
	if err != nil {
		return ExplorerReport{}, err
	}
	limit := e.ToolLimit
	if limit <= 0 {
		limit = 40
	}
	if len(outcome.ToolCalls) > limit {
		_ = e.Session.Abort(context.Background())
		return ExplorerReport{}, fmt.Errorf("executor used %d tool calls, limit is %d", len(outcome.ToolCalls), limit)
	}
	info, err := os.Stat(reportPath)
	if err != nil {
		return ExplorerReport{}, fmt.Errorf("stat explorer report: %w", err)
	}
	if info.Size() > MaxExplorerReportBytes {
		return ExplorerReport{}, fmt.Errorf("explorer report is too large: %d bytes", info.Size())
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		return ExplorerReport{}, fmt.Errorf("read explorer report: %w", err)
	}
	return ParseExplorerReport(data, item)
}

func parseTurnEvent(data json.RawMessage) (string, []string) {
	var payload struct {
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
				Name string `json:"name"`
			} `json:"content"`
		} `json:"message"`
		ToolResults []struct {
			ToolName string `json:"toolName"`
		} `json:"toolResults"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", nil
	}
	var texts []string
	var contentTools []string
	for _, part := range payload.Message.Content {
		switch part.Type {
		case "text":
			if strings.TrimSpace(part.Text) != "" {
				texts = append(texts, part.Text)
			}
		case "toolCall":
			if part.Name != "" {
				contentTools = append(contentTools, part.Name)
			}
		}
	}
	tools := contentTools
	if len(tools) == 0 {
		for _, result := range payload.ToolResults {
			if result.ToolName != "" {
				tools = append(tools, result.ToolName)
			}
		}
	}
	return strings.Join(texts, "\n"), tools
}

func buildPlanPrompt(request PlanRequest) string {
	var out strings.Builder
	out.WriteString("You are the planner for an authorized security task.\n")
	out.WriteString("Use only the bounded blackboard below. Do not claim task success.\n\n")
	fmt.Fprintf(&out, "Objective: %s\n", request.Objective)
	if request.Progress.Total > 0 {
		fmt.Fprintf(&out, "Authoritative progress: %d/%d\n", request.Progress.Progress, request.Progress.Total)
	}
	out.WriteString("Authorized targets:\n")
	for _, target := range request.Targets {
		fmt.Fprintf(&out, "- id=%s addresses=%s\n", target.ID, strings.Join(target.Addresses, ", "))
	}
	out.WriteString("\nFacts:\n")
	limit := len(request.Facts)
	if limit > MaxDigestFacts {
		limit = MaxDigestFacts
	}
	if limit == 0 {
		out.WriteString("- none\n")
	}
	for _, fact := range request.Facts[:limit] {
		if fact.Trust == FactRefuted {
			continue
		}
		label := string(fact.Trust)
		if fact.Contested {
			label = "contested"
		}
		fmt.Fprintf(&out, "- [%s %s] %s\n", fact.ID, label, truncateBytes(fact.Claim, MaxDigestClaimBytes))
	}
	out.WriteString("\nRecent frontier outcomes:\n")
	if len(request.RecentItems) == 0 {
		out.WriteString("- none\n")
	}
	start := len(request.RecentItems) - 20
	if start < 0 {
		start = 0
	}
	for _, item := range request.RecentItems[start:] {
		fmt.Fprintf(&out, "- [%s] %s -> %s\n", item.Status, item.Question, item.TargetHint)
	}
	out.WriteString(`

Return exactly one JSON object and no prose:
{"next_items":[{"question":"one falsifiable next question","target":"authorized target id/address","expected_evidence":"what evidence would settle it","anchors":["corroborated fact id"]}]}
Rules:
- Propose at most 3 independent frontiers.
- Each question must produce concrete evidence.
- The target field must contain one exact authorized ID or address, never a label plus address.
- Anchors may reference only corroborated facts.
- Do not repeat a closed frontier or claim objective success.
`)
	return out.String()
}

var _ Planner = (*SessionPlanner)(nil)
var _ Explorer = (*SessionExplorer)(nil)
