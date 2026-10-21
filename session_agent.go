package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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

// TurnLimits are enforced while the turn is running, not after it settles.
type TurnLimits struct {
	MaxToolCalls int
	ToolTimeout  time.Duration
}

const DefaultToolCallTimeout = 30 * time.Second

// RunTurn sends one prompt and waits for agent_settled, collecting only the
// assistant text and tool-call names needed by the role adapter.
func RunTurn(ctx context.Context, session AgentSession, prompt string, sink EventSink) (TurnOutcome, error) {
	return RunTurnWithLimits(ctx, session, prompt, sink, TurnLimits{})
}

// RunTurnWithLimits aborts a session as soon as a hard tool or tool-time limit
// is crossed. A final accounting pass is only a fallback for sessions that do
// not emit live tool_execution_start events.
func RunTurnWithLimits(
	ctx context.Context,
	session AgentSession,
	prompt string,
	sink EventSink,
	limits TurnLimits,
) (TurnOutcome, error) {
	if session == nil {
		return TurnOutcome{}, errors.New("agent session is required")
	}
	if err := session.Prompt(ctx, prompt); err != nil {
		if ctx.Err() != nil {
			abortAndDrain(session, sink)
		}
		return TurnOutcome{}, err
	}
	var outcome TurnOutcome
	var toolTimer *time.Timer
	var toolDeadline <-chan time.Time
	stopToolTimer := func() {
		if toolTimer != nil {
			if !toolTimer.Stop() {
				select {
				case <-toolTimer.C:
				default:
				}
			}
			toolTimer = nil
		}
		toolDeadline = nil
	}
	defer stopToolTimer()
	liveToolCalls := 0
	for {
		select {
		case <-ctx.Done():
			abortAndDrain(session, sink)
			return TurnOutcome{}, ctx.Err()
		case <-toolDeadline:
			abortAndDrain(session, sink)
			return TurnOutcome{}, fmt.Errorf("tool timeout after %s", limits.ToolTimeout)
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
			case "tool_execution_start":
				if !isReportWrite(event.Data) {
					liveToolCalls++
					if limits.MaxToolCalls > 0 && liveToolCalls > limits.MaxToolCalls {
						abortAndDrain(session, sink)
						return TurnOutcome{}, fmt.Errorf(
							"tool call limit exceeded: %d > %d", liveToolCalls, limits.MaxToolCalls,
						)
					}
				}
				if limits.ToolTimeout > 0 {
					stopToolTimer()
					toolTimer = time.NewTimer(limits.ToolTimeout)
					toolDeadline = toolTimer.C
				}
			case "tool_execution_end":
				stopToolTimer()
			case "turn_end":
				text, tools := parseTurnEvent(event.Data)
				if text != "" {
					outcome.AssistantText = text
				}
				outcome.ToolCalls = append(outcome.ToolCalls, tools...)
				if liveToolCalls == 0 && limits.MaxToolCalls > 0 && len(outcome.ToolCalls) > limits.MaxToolCalls {
					abortAndDrain(session, sink)
					return TurnOutcome{}, fmt.Errorf(
						"tool call limit exceeded: %d > %d", len(outcome.ToolCalls), limits.MaxToolCalls,
					)
				}
				stopToolTimer()
			case "agent_settled":
				return outcome, nil
			}
		}
	}
}

func isReportWrite(data json.RawMessage) bool {
	var payload struct {
		ToolName string `json:"toolName"`
		Args     struct {
			Path string `json:"path"`
		} `json:"args"`
	}
	if json.Unmarshal(data, &payload) != nil || payload.ToolName != "write" {
		return false
	}
	path := filepath.Clean(payload.Args.Path)
	return path == ReportFileName || path == filepath.Join(string(filepath.Separator), "workspace", ReportFileName)
}

func abortAndDrain(session AgentSession, sink EventSink) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = session.Abort(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-session.Events():
			if !ok {
				return
			}
			if sink != nil {
				sink(event)
			}
			if event.Type == "agent_settled" {
				return
			}
		}
	}
}

// SessionPlanner adapts an AgentSession to the Planner contract. It receives a
// bounded blackboard snapshot and returns JSON only.
type SessionPlanner struct {
	Session   AgentSession
	Sink      EventSink
	Timeout   time.Duration
	ToolLimit int
}

const (
	DefaultPlannerTimeout   = 120 * time.Second
	DefaultPlannerToolLimit = 4
)

func (p *SessionPlanner) Plan(ctx context.Context, request PlanRequest) (PlanOutput, error) {
	if p == nil || p.Session == nil {
		return PlanOutput{}, errors.New("planner session is required")
	}
	resettable, ok := p.Session.(ResettableSession)
	if !ok {
		return PlanOutput{}, errors.New("planner session does not support fresh context reset")
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = DefaultPlannerTimeout
	}
	planCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := resettable.Reset(planCtx); err != nil {
		return PlanOutput{}, fmt.Errorf("reset planner session: %w", err)
	}
	toolLimit := p.ToolLimit
	if toolLimit <= 0 {
		toolLimit = DefaultPlannerToolLimit
	}
	outcome, err := RunTurnWithLimits(planCtx, p.Session, buildPlanPrompt(request), p.Sink, TurnLimits{
		MaxToolCalls: toolLimit,
		ToolTimeout:  DefaultToolCallTimeout,
	})
	if err != nil {
		if errors.Is(planCtx.Err(), context.DeadlineExceeded) {
			return PlanOutput{}, fmt.Errorf("planner timed out after %s: %w", timeout, err)
		}
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
	limit := item.MaxTools
	if limit <= 0 {
		limit = e.ToolLimit
	}
	if limit <= 0 {
		limit = DefaultFrontierToolLimit
	}
	if limit > MaxFrontierToolLimit {
		limit = MaxFrontierToolLimit
	}
	duration := time.Duration(item.MaxDurationSeconds) * time.Second
	if duration <= 0 {
		duration = DefaultFrontierDurationSeconds * time.Second
	}
	if duration > MaxFrontierDurationSeconds*time.Second {
		duration = MaxFrontierDurationSeconds * time.Second
	}
	frontierCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	_, err := RunTurnWithLimits(frontierCtx, e.Session, brief, e.Sink, TurnLimits{
		MaxToolCalls: limit,
		ToolTimeout:  DefaultToolCallTimeout,
	})
	if err != nil {
		if errors.Is(frontierCtx.Err(), context.DeadlineExceeded) {
			return ExplorerReport{}, fmt.Errorf("frontier timed out after %s: %w", duration, err)
		}
		return ExplorerReport{}, err
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
	out.WriteString("Use only the bounded blackboard below. Do not use tools. Do not claim task success.\n\n")
	fmt.Fprintf(&out, "Objective: %s\n", request.Objective)
	if request.Progress.Total > 0 {
		fmt.Fprintf(&out, "Authoritative progress: %d/%d\n", request.Progress.Progress, request.Progress.Total)
	}
	out.WriteString("\nTask brief (the problem statement the Planner owns):\n")
	if len(request.Metadata) == 0 {
		out.WriteString("- none\n")
	}
	for _, key := range sortedMetadataKeys(request.Metadata) {
		fmt.Fprintf(&out, "- %s=%s\n", key, truncateBytes(request.Metadata[key], MaxDigestClaimBytes))
	}
	out.WriteString("\nAuthorized targets:\n")
	for _, target := range request.Targets {
		fmt.Fprintf(&out, "- id=%s addresses=%s\n", target.ID, strings.Join(target.Addresses, ", "))
		for _, key := range sortedMetadataKeys(target.Metadata) {
			fmt.Fprintf(&out, "  %s=%s\n", key, truncateBytes(target.Metadata[key], MaxDigestClaimBytes))
		}
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
		fmt.Fprintf(
			&out, "- [%s] %s -> %s probe=%s params=%s success=%s failure=%s\n",
			item.Status, item.Question, item.TargetHint, item.ProbeKey,
			strings.Join(item.Params, ","), item.ExpectedEvidence, item.FailureEvidence,
		)
	}
	out.WriteString("\nRecent reports (untrusted observations):\n")
	if len(request.Reports) == 0 {
		out.WriteString("- none\n")
	}
	reportStart := len(request.Reports) - 8
	if reportStart < 0 {
		reportStart = 0
	}
	for _, report := range request.Reports[reportStart:] {
		fmt.Fprintf(&out, "- item=%s verdict=%s\n", report.ItemID, report.Verdict)
		for index, claim := range report.Claims {
			if index == 2 {
				break
			}
			fmt.Fprintf(
				&out, "  claim=%s evidence=%s\n",
				truncateBytes(claim.Claim, MaxDigestClaimBytes),
				truncateBytes(claim.Evidence, MaxDigestClaimBytes),
			)
		}
		for index, reportErr := range report.Errors {
			if index == 2 {
				break
			}
			fmt.Fprintf(&out, "  error=%s\n", truncateBytes(reportErr, MaxDigestClaimBytes))
		}
	}
	out.WriteString(`

Return exactly one JSON object and no prose:
{"next_items":[{"question":"one falsifiable hypothesis","target":"one exact authorized target id/address","probe_key":"lowercase.operation","params":["one bounded value"],"expected_evidence":"evidence that supports the hypothesis","failure_evidence":"evidence that refutes it","anchors":["corroborated fact id"]}]}
Rules:
- Propose exactly 2 independent frontiers when possible, and never more than 3.
- The Controller dispatches up to 2 frontiers concurrently to two fresh-context Executors.
- Each frontier is one bounded experiment: one hypothesis, one probe, explicit parameters,
  and both success and failure evidence.
- Use concrete probes such as http.get with a known path, tcp.connect with one known
  host:port, or service.banner with one known host:port.
- Do not propose broad port scans, content discovery, fuzzing, credential matrices,
  "map the attack surface", or any task without a stopping condition.
- The target field must contain one exact authorized ID or address, never a label plus address.
- Probe parameters must be machine literals, not wordlists, ranges, wildcards or
  sentences: an address, a path, a username, or a lowercase hex byte string such
  as fffb18fffb03 when the experiment must send exact protocol bytes. Never put
  prose, spaces or control characters in params.
- If a round was dropped for "single literal values" or "control characters",
  rewrite the same experiment with hex or plain literal params instead of
  restating it in prose.
- Anchors may reference only corroborated facts.
- Do not repeat a closed frontier or claim objective success.
- Write "question", "expected_evidence" and "failure_evidence" in Simplified
  Chinese (简体中文) so a human operator can read the blackboard. Keep technical
  literals verbatim: target ids, addresses, probe_key and params.
`)
	return out.String()
}

func sortedMetadataKeys(metadata map[string]string) []string {
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

var _ Planner = (*SessionPlanner)(nil)
var _ Explorer = (*SessionExplorer)(nil)
