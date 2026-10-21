package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type scriptedAgentSession struct {
	events       chan AgentEvent
	onPrompt     func(string) error
	mu           sync.Mutex
	prompts      []string
	aborts       int
	abortSettles bool
}

func newScriptedAgentSession() *scriptedAgentSession {
	return &scriptedAgentSession{events: make(chan AgentEvent, 16), abortSettles: true}
}

func (s *scriptedAgentSession) Prompt(_ context.Context, message string) error {
	s.mu.Lock()
	s.prompts = append(s.prompts, message)
	s.mu.Unlock()
	if s.onPrompt != nil {
		return s.onPrompt(message)
	}
	return nil
}

func (s *scriptedAgentSession) Steer(context.Context, string) error { return nil }
func (s *scriptedAgentSession) Events() <-chan AgentEvent           { return s.events }
func (s *scriptedAgentSession) Wait(context.Context) (AgentResult, error) {
	return AgentResult{Status: StatusSucceeded}, nil
}
func (s *scriptedAgentSession) Abort(context.Context) error {
	s.mu.Lock()
	s.aborts++
	settle := s.abortSettles
	s.mu.Unlock()
	if settle {
		s.events <- AgentEvent{Type: "agent_settled", Data: json.RawMessage(`{"type":"agent_settled","aborted":true}`)}
	}
	return nil
}
func (s *scriptedAgentSession) Close(context.Context) error { return nil }
func (s *scriptedAgentSession) Reset(context.Context) error { return nil }

func TestRunTurnReturnsAssistantTextAndPersistsEvents(t *testing.T) {
	session := newScriptedAgentSession()
	session.events <- AgentEvent{Type: "message_start", Data: json.RawMessage(`{"type":"message_start"}`)}
	session.events <- AgentEvent{Type: "turn_end", Data: json.RawMessage(`{
		"type":"turn_end",
		"message":{"role":"assistant","content":[
			{"type":"text","text":"{\"next_items\":[]}"},
			{"type":"toolCall","name":"read"}
		]},
		"toolResults":[{"toolName":"read","content":[],"isError":false}]
	}`)}
	session.events <- AgentEvent{Type: "agent_settled", Data: json.RawMessage(`{"type":"agent_settled"}`)}
	var sunk []AgentEvent
	outcome, err := RunTurn(context.Background(), session, "plan", func(event AgentEvent) {
		sunk = append(sunk, event)
	})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if outcome.AssistantText != `{"next_items":[]}` {
		t.Fatalf("assistant text = %q", outcome.AssistantText)
	}
	if len(outcome.ToolCalls) != 1 || outcome.ToolCalls[0] != "read" {
		t.Fatalf("tool calls = %v", outcome.ToolCalls)
	}
	if len(sunk) != 3 {
		t.Fatalf("sunk events = %d, want 3", len(sunk))
	}
}

func TestSessionExplorerReadsReportFromWorkspace(t *testing.T) {
	workspace := t.TempDir()
	item := FrontierItem{
		ID: "frontier-1", Question: "Inspect root", TargetHint: "web", ExpectedEvidence: "HTTP response",
	}
	session := newScriptedAgentSession()
	session.onPrompt = func(string) error {
		report := ExplorerReport{ItemID: item.ID, Verdict: VerdictInconclusive}
		data, _ := json.Marshal(report)
		if err := os.WriteFile(filepath.Join(workspace, ReportFileName), data, 0o600); err != nil {
			return err
		}
		session.events <- AgentEvent{Type: "agent_settled", Data: json.RawMessage(`{"type":"agent_settled"}`)}
		return nil
	}
	explorer := &SessionExplorer{
		Session: session, IDValue: "executor", WorkspaceDir: workspace,
	}
	report, err := explorer.Investigate(context.Background(), item, "brief")
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if report.ItemID != item.ID || report.Verdict != VerdictInconclusive {
		t.Fatalf("report = %+v", report)
	}
}

func TestSessionExplorerRejectsMismatchedReportID(t *testing.T) {
	workspace := t.TempDir()
	item := FrontierItem{ID: "frontier-1"}
	session := newScriptedAgentSession()
	session.onPrompt = func(string) error {
		_ = os.WriteFile(filepath.Join(workspace, ReportFileName),
			[]byte(`{"item_id":"other","verdict":"inconclusive"}`), 0o600)
		session.events <- AgentEvent{Type: "agent_settled", Data: json.RawMessage(`{"type":"agent_settled"}`)}
		return nil
	}
	explorer := &SessionExplorer{Session: session, IDValue: "executor", WorkspaceDir: workspace}
	_, err := explorer.Investigate(context.Background(), item, "brief")
	if err == nil || !strings.Contains(err.Error(), "item_id") {
		t.Fatalf("err = %v, want item_id mismatch", err)
	}
}

func TestSessionPlannerParsesAssistantJSON(t *testing.T) {
	session := newScriptedAgentSession()
	session.onPrompt = func(string) error {
		session.events <- AgentEvent{Type: "turn_end", Data: json.RawMessage(`{
			"type":"turn_end",
			"message":{"role":"assistant","content":[
				{"type":"text","text":"{\"next_items\":[{\"question\":\"Inspect root\",\"target\":\"web\",\"expected_evidence\":\"HTTP response\"}]}"}
			]}
		}`)}
		session.events <- AgentEvent{Type: "agent_settled", Data: json.RawMessage(`{"type":"agent_settled"}`)}
		return nil
	}
	planner := &SessionPlanner{Session: session}
	output, err := planner.Plan(context.Background(), PlanRequest{
		Objective: "find the flag",
		Targets:   []Target{{ID: "web", Addresses: []string{"10.0.0.5:80"}}},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(output.NextItems) != 1 || output.NextItems[0].Question != "Inspect root" {
		t.Fatalf("output = %+v", output)
	}
}

func TestBuildPlanPromptIncludesBoundedExperimentOutcomes(t *testing.T) {
	prompt := buildPlanPrompt(PlanRequest{
		Objective: "find the flag",
		Targets:   []Target{{ID: "web", Addresses: []string{"10.0.0.5:80"}}},
		RecentItems: []FrontierItem{{
			Question: "Does /admin expose a form?", TargetHint: "web",
			ProbeKey: "http.get", Params: []string{"/admin"},
			ExpectedEvidence: "login form", FailureEvidence: "HTTP 404",
			Status: FrontierDone,
		}},
		Reports: []ExplorerReport{{
			ItemID: "frontier-a", Verdict: VerdictRefuted,
			Claims: []ExplorerClaim{{
				Claim: "admin path is absent", Evidence: "GET /admin returned HTTP 404",
			}},
		}},
	})
	for _, want := range []string{
		"probe=http.get params=/admin", "failure=HTTP 404",
		"verdict=refuted", "GET /admin returned HTTP 404", "Do not use tools",
		"简体中文", "machine literal", "hex",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("planner prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildPlanPromptIncludesTaskAndTargetMetadata(t *testing.T) {
	prompt := buildPlanPrompt(PlanRequest{
		Objective: "Authorized TSec challenge c-07.",
		Metadata: map[string]string{
			"challenge_code":        "c-07",
			"challenge_description": "一个运行在 23 端口的 Telnet 服务，flag 位于远端 shell 中。",
			"flag_count":            "1",
		},
		Targets: []Target{{
			ID: "c-07", Addresses: []string{"10.0.169.88:23"},
			Metadata: map[string]string{"challenge_code": "c-07", "network": "host"},
		}},
	})
	for _, want := range []string{
		"challenge_description", "flag 位于远端 shell 中", "challenge_code=c-07", "network=host",
		"10.0.169.88:23",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("planner prompt missing %q:\n%s", want, prompt)
		}
	}
}

type nonResettableSession struct {
	AgentSession
}

func TestSessionPlannerRequiresResettableSession(t *testing.T) {
	planner := &SessionPlanner{Session: nonResettableSession{AgentSession: newScriptedAgentSession()}}
	if _, err := planner.Plan(context.Background(), PlanRequest{}); err == nil || !strings.Contains(err.Error(), "reset") {
		t.Fatalf("err = %v, want fresh-context reset error", err)
	}
}

func TestSessionPlannerHonorsPlanningTimeout(t *testing.T) {
	session := newScriptedAgentSession()
	session.onPrompt = func(string) error { return nil }
	planner := &SessionPlanner{Session: session, Timeout: 20 * time.Millisecond}
	started := time.Now()
	_, err := planner.Plan(context.Background(), PlanRequest{Objective: "find the flag"})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want planner timeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("planner timeout took %s, want under 1s", elapsed)
	}
	if session.aborts != 1 {
		t.Fatalf("aborts = %d, want 1", session.aborts)
	}
}

func TestSessionPlannerEnforcesToolBudget(t *testing.T) {
	session := newScriptedAgentSession()
	session.events <- AgentEvent{Type: "tool_execution_start", Data: json.RawMessage(`{"toolName":"read"}`)}
	session.events <- AgentEvent{Type: "tool_execution_end", Data: json.RawMessage(`{"toolName":"read"}`)}
	session.events <- AgentEvent{Type: "tool_execution_start", Data: json.RawMessage(`{"toolName":"read"}`)}
	session.events <- AgentEvent{Type: "tool_execution_end", Data: json.RawMessage(`{"toolName":"read"}`)}
	session.events <- AgentEvent{Type: "tool_execution_start", Data: json.RawMessage(`{"toolName":"read"}`)}
	planner := &SessionPlanner{Session: session, ToolLimit: 2}
	_, err := planner.Plan(context.Background(), PlanRequest{Objective: "find the flag"})
	if err == nil || !strings.Contains(err.Error(), "tool call limit") {
		t.Fatalf("err = %v, want planner tool limit", err)
	}
}

func TestRunTurnHonorsCancellation(t *testing.T) {
	session := newScriptedAgentSession()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	_, err := RunTurn(ctx, session, "blocked", nil)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if time.Since(started) > time.Second {
		t.Fatal("RunTurn did not honor cancellation promptly")
	}
}

func TestRunTurnCountsRepeatedToolCalls(t *testing.T) {
	session := newScriptedAgentSession()
	session.events <- AgentEvent{Type: "turn_end", Data: json.RawMessage(`{
		"type":"turn_end",
		"message":{"role":"assistant","content":[
			{"type":"toolCall","name":"bash"},
			{"type":"toolCall","name":"bash"},
			{"type":"toolCall","name":"bash"}
		]}
	}`)}
	session.events <- AgentEvent{Type: "agent_settled", Data: json.RawMessage(`{"type":"agent_settled"}`)}
	outcome, err := RunTurn(context.Background(), session, "execute", nil)
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(outcome.ToolCalls) != 3 {
		t.Fatalf("tool calls = %d, want 3", len(outcome.ToolCalls))
	}
}

func TestRunTurnWithLimitsStopsWhenToolBudgetIsExceeded(t *testing.T) {
	session := newScriptedAgentSession()
	session.events <- AgentEvent{Type: "tool_execution_start", Data: json.RawMessage(`{"toolName":"bash"}`)}
	session.events <- AgentEvent{Type: "tool_execution_end", Data: json.RawMessage(`{"toolName":"bash"}`)}
	session.events <- AgentEvent{Type: "tool_execution_start", Data: json.RawMessage(`{"toolName":"bash"}`)}
	_, err := RunTurnWithLimits(context.Background(), session, "execute", nil, TurnLimits{
		MaxToolCalls: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "tool call limit") {
		t.Fatalf("err = %v, want tool call limit", err)
	}
	if session.aborts != 1 {
		t.Fatalf("aborts = %d, want 1", session.aborts)
	}
	if event, ok := nextEvent(session.events); ok {
		t.Fatalf("aborted turn left stale event %q for the next round", event.Type)
	}
}

func TestRunTurnWithLimitsStopsAnOverlongToolCall(t *testing.T) {
	session := newScriptedAgentSession()
	session.events <- AgentEvent{Type: "tool_execution_start", Data: json.RawMessage(`{"toolName":"bash"}`)}
	started := time.Now()
	_, err := RunTurnWithLimits(context.Background(), session, "execute", nil, TurnLimits{
		MaxToolCalls: 5,
		ToolTimeout:  20 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "tool timeout") {
		t.Fatalf("err = %v, want tool timeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("tool timeout took %s, want under 1s", elapsed)
	}
	if session.aborts != 1 {
		t.Fatalf("aborts = %d, want 1", session.aborts)
	}
	if event, ok := nextEvent(session.events); ok {
		t.Fatalf("timed out turn left stale event %q for the next round", event.Type)
	}
}

func TestRunTurnWithLimitsDoesNotChargeReportWriteToProbeBudget(t *testing.T) {
	session := newScriptedAgentSession()
	session.events <- AgentEvent{Type: "tool_execution_start", Data: json.RawMessage(`{"toolName":"bash"}`)}
	session.events <- AgentEvent{Type: "tool_execution_end", Data: json.RawMessage(`{"toolName":"bash"}`)}
	session.events <- AgentEvent{
		Type: "tool_execution_start",
		Data: json.RawMessage(`{"toolName":"write","args":{"path":"/workspace/report.json"}}`),
	}
	session.events <- AgentEvent{Type: "tool_execution_end", Data: json.RawMessage(`{"toolName":"write"}`)}
	session.events <- AgentEvent{Type: "agent_settled", Data: json.RawMessage(`{"type":"agent_settled"}`)}
	_, err := RunTurnWithLimits(context.Background(), session, "execute", nil, TurnLimits{
		MaxToolCalls: 1,
	})
	if err != nil {
		t.Fatalf("report write must not exceed the probe budget: %v", err)
	}
}

func nextEvent(events <-chan AgentEvent) (AgentEvent, bool) {
	select {
	case event, ok := <-events:
		return event, ok
	default:
		return AgentEvent{}, false
	}
}

func TestSessionExplorerRejectsOversizedReport(t *testing.T) {
	workspace := t.TempDir()
	item := FrontierItem{ID: "frontier-1"}
	session := newScriptedAgentSession()
	session.onPrompt = func(string) error {
		path := filepath.Join(workspace, ReportFileName)
		if err := os.WriteFile(path, make([]byte, MaxExplorerReportBytes+1), 0o600); err != nil {
			return err
		}
		session.events <- AgentEvent{Type: "agent_settled", Data: json.RawMessage(`{"type":"agent_settled"}`)}
		return nil
	}
	explorer := &SessionExplorer{Session: session, IDValue: "executor", WorkspaceDir: workspace}
	_, err := explorer.Investigate(context.Background(), item, "brief")
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want oversized report error", err)
	}
}

func TestSessionExplorerEnforcesItemToolBudget(t *testing.T) {
	workspace := t.TempDir()
	item := FrontierItem{ID: "frontier-1", MaxTools: 1, MaxDurationSeconds: 90}
	session := newScriptedAgentSession()
	session.events <- AgentEvent{Type: "tool_execution_start", Data: json.RawMessage(`{"toolName":"read"}`)}
	session.events <- AgentEvent{Type: "tool_execution_end", Data: json.RawMessage(`{"toolName":"read"}`)}
	session.events <- AgentEvent{Type: "tool_execution_start", Data: json.RawMessage(`{"toolName":"bash"}`)}
	explorer := &SessionExplorer{Session: session, IDValue: "executor", WorkspaceDir: workspace}
	_, err := explorer.Investigate(context.Background(), item, "brief")
	if err == nil || !strings.Contains(err.Error(), "tool call limit") {
		t.Fatalf("err = %v, want item tool budget error", err)
	}
}

func TestSessionExplorerEnforcesItemDuration(t *testing.T) {
	workspace := t.TempDir()
	item := FrontierItem{ID: "frontier-1", MaxTools: 5, MaxDurationSeconds: 1}
	session := newScriptedAgentSession()
	session.onPrompt = func(string) error { return nil }
	explorer := &SessionExplorer{Session: session, IDValue: "executor", WorkspaceDir: workspace}
	started := time.Now()
	_, err := explorer.Investigate(context.Background(), item, "brief")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want frontier timeout", err)
	}
	if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
		t.Fatalf("duration enforcement took %s, want about 1s", elapsed)
	}
}
