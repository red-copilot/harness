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
	events   chan AgentEvent
	onPrompt func(string) error
	mu       sync.Mutex
	prompts  []string
}

func newScriptedAgentSession() *scriptedAgentSession {
	return &scriptedAgentSession{events: make(chan AgentEvent, 16)}
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
func (s *scriptedAgentSession) Abort(context.Context) error { return nil }
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

type nonResettableSession struct {
	AgentSession
}

func TestSessionPlannerRequiresResettableSession(t *testing.T) {
	planner := &SessionPlanner{Session: nonResettableSession{AgentSession: newScriptedAgentSession()}}
	if _, err := planner.Plan(context.Background(), PlanRequest{}); err == nil || !strings.Contains(err.Error(), "reset") {
		t.Fatalf("err = %v, want fresh-context reset error", err)
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
