package exploration

import (
	"context"
	"encoding/json"
	"github.com/red-copilot/harness"
	"strings"
	"sync"
	"testing"
	"time"
)

type scriptedAgentSession struct {
	events       chan harness.AgentEvent
	onPrompt     func(string) error
	mu           sync.Mutex
	prompts      []string
	aborts       int
	abortSettles bool
}

func newScriptedAgentSession() *scriptedAgentSession {
	return &scriptedAgentSession{events: make(chan harness.AgentEvent, 16), abortSettles: true}
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
func (s *scriptedAgentSession) Events() <-chan harness.AgentEvent   { return s.events }
func (s *scriptedAgentSession) Wait(context.Context) (harness.AgentResult, error) {
	return harness.AgentResult{Status: harness.StatusSucceeded}, nil
}
func (s *scriptedAgentSession) Abort(context.Context) error {
	s.mu.Lock()
	s.aborts++
	settle := s.abortSettles
	s.mu.Unlock()
	if settle {
		s.events <- harness.AgentEvent{Type: "agent_settled", Data: json.RawMessage(`{"type":"agent_settled","aborted":true}`)}
	}
	return nil
}
func (s *scriptedAgentSession) Close(context.Context) error { return nil }
func (s *scriptedAgentSession) Reset(context.Context) error { return nil }
func TestRunTurnReturnsAssistantTextAndPersistsEvents(t *testing.T) {
	session := newScriptedAgentSession()
	session.events <- harness.AgentEvent{Type: "message_start", Data: json.RawMessage(`{"type":"message_start"}`)}
	session.events <- harness.AgentEvent{Type: "turn_end", Data: json.RawMessage(`{
		"type":"turn_end",
		"message":{"role":"assistant","content":[
			{"type":"text","text":"{\"next_items\":[]}"},
			{"type":"toolCall","name":"read"}
		]},
		"toolResults":[{"toolName":"read","content":[],"isError":false}]
	}`)}
	session.events <- harness.AgentEvent{Type: "agent_settled", Data: json.RawMessage(`{"type":"agent_settled"}`)}
	var sunk []harness.AgentEvent
	outcome, err := RunTurn(context.Background(), session, "plan", func(event harness.AgentEvent) error {
		sunk = append(sunk, event)
		return nil
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
	session.events <- harness.AgentEvent{Type: "turn_end", Data: json.RawMessage(`{
		"type":"turn_end",
		"message":{"role":"assistant","content":[
			{"type":"toolCall","name":"bash"},
			{"type":"toolCall","name":"bash"},
			{"type":"toolCall","name":"bash"}
		]}
	}`)}
	session.events <- harness.AgentEvent{Type: "agent_settled", Data: json.RawMessage(`{"type":"agent_settled"}`)}
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
	session.events <- harness.AgentEvent{Type: "tool_execution_start", Data: json.RawMessage(`{"toolName":"bash"}`)}
	session.events <- harness.AgentEvent{Type: "tool_execution_end", Data: json.RawMessage(`{"toolName":"bash"}`)}
	session.events <- harness.AgentEvent{Type: "tool_execution_start", Data: json.RawMessage(`{"toolName":"bash"}`)}
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
	session.events <- harness.AgentEvent{Type: "tool_execution_start", Data: json.RawMessage(`{"toolName":"bash"}`)}
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
	session.events <- harness.AgentEvent{Type: "tool_execution_start", Data: json.RawMessage(`{"toolName":"bash"}`)}
	session.events <- harness.AgentEvent{Type: "tool_execution_end", Data: json.RawMessage(`{"toolName":"bash"}`)}
	session.events <- harness.AgentEvent{
		Type: "tool_execution_start",
		Data: json.RawMessage(`{"toolName":"write","args":{"path":"/workspace/report.json"}}`),
	}
	session.events <- harness.AgentEvent{Type: "tool_execution_end", Data: json.RawMessage(`{"toolName":"write"}`)}
	session.events <- harness.AgentEvent{Type: "agent_settled", Data: json.RawMessage(`{"type":"agent_settled"}`)}
	_, err := RunTurnWithLimits(context.Background(), session, "execute", nil, TurnLimits{
		MaxToolCalls: 1,
	})
	if err != nil {
		t.Fatalf("report write must not exceed the probe budget: %v", err)
	}
}

func nextEvent(events <-chan harness.AgentEvent) (harness.AgentEvent, bool) {
	select {
	case event, ok := <-events:
		return event, ok
	default:
		return harness.AgentEvent{}, false
	}
}
