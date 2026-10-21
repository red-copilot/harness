package harness_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

func TestRunAgentTurnSettlesOnlyOnAgentSettled(t *testing.T) {
	session := &fakeSession{events: make(chan harness.AgentEvent, 4)}
	session.prompt = func(context.Context, string) error {
		session.events <- harness.AgentEvent{Type: "turn_end", Kind: harness.AgentEventMessage, Text: "done", Data: json.RawMessage(`{"message":{"content":[{"type":"text","text":"done"}]}}`)}
		session.events <- harness.AgentEvent{Type: "agent_end", Kind: harness.AgentEventEnd, Data: json.RawMessage(`{"type":"agent_end"}`)}
		session.events <- harness.AgentEvent{Type: "agent_settled", Kind: harness.AgentEventSettled, Data: json.RawMessage(`{"type":"agent_settled"}`)}
		return nil
	}
	result, err := harness.RunAgentTurn(context.Background(), session, "go", harness.TurnLimits{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.AssistantText != "done" {
		t.Fatalf("assistant = %q", result.AssistantText)
	}
}

func TestRunAgentTurnEnforcesToolLimitAndTimeout(t *testing.T) {
	t.Run("limit", func(t *testing.T) {
		session := &fakeSession{events: make(chan harness.AgentEvent, 4)}
		session.prompt = func(context.Context, string) error {
			session.events <- harness.AgentEvent{Type: "tool_execution_start", Kind: harness.AgentEventToolStart, Tool: "bash", Data: json.RawMessage(`{"toolName":"bash"}`)}
			session.events <- harness.AgentEvent{Type: "tool_execution_start", Kind: harness.AgentEventToolStart, Tool: "bash", Data: json.RawMessage(`{"toolName":"bash"}`)}
			return nil
		}
		_, err := harness.RunAgentTurn(context.Background(), session, "go", harness.TurnLimits{MaxToolCalls: 1}, nil)
		if err == nil {
			t.Fatal("expected tool limit error")
		}
	})
	t.Run("timeout", func(t *testing.T) {
		session := &fakeSession{events: make(chan harness.AgentEvent, 4)}
		session.prompt = func(context.Context, string) error {
			session.events <- harness.AgentEvent{Type: "tool_execution_start", Kind: harness.AgentEventToolStart, Tool: "bash", Data: json.RawMessage(`{"toolName":"bash"}`)}
			return nil
		}
		_, err := harness.RunAgentTurn(context.Background(), session, "go", harness.TurnLimits{MaxToolCalls: 2, ToolTimeout: 10 * time.Millisecond}, nil)
		if err == nil || !strings.Contains(err.Error(), "tool timeout") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestRunAgentTurnConsumesOnlyNormalizedSignals(t *testing.T) {
	session := &fakeSession{events: make(chan harness.AgentEvent, 8)}
	session.prompt = func(context.Context, string) error {
		// Adapter-native Pi records: the core must not interpret these itself.
		// Before normalization moved into the adapter, two raw tool starts
		// tripped MaxToolCalls and the raw agent_settled settled the turn.
		session.events <- harness.AgentEvent{Type: "tool_execution_start", Data: json.RawMessage(`{"toolName":"bash","args":{"path":"/workspace/raw"}}`)}
		session.events <- harness.AgentEvent{Type: "tool_execution_start", Data: json.RawMessage(`{"toolName":"bash","args":{"path":"/workspace/raw"}}`)}
		session.events <- harness.AgentEvent{Type: "turn_end", Data: json.RawMessage(`{"message":{"content":[{"type":"text","text":"raw"}]}}`)}
		session.events <- harness.AgentEvent{Type: "agent_settled", Data: json.RawMessage(`{"type":"agent_settled"}`)}
		// The same records after adapter normalization.
		session.events <- harness.AgentEvent{Type: "tool_execution_start", Kind: harness.AgentEventToolStart, Tool: "bash"}
		session.events <- harness.AgentEvent{Type: "turn_end", Kind: harness.AgentEventMessage, Text: "normalized"}
		session.events <- harness.AgentEvent{Type: "agent_settled", Kind: harness.AgentEventSettled}
		return nil
	}
	result, err := harness.RunAgentTurn(context.Background(), session, "go", harness.TurnLimits{MaxToolCalls: 1}, nil)
	if err != nil {
		t.Fatalf("raw adapter records must not drive the turn: %v", err)
	}
	if result.AssistantText != "normalized" {
		t.Fatalf("assistant = %q, want the normalized message", result.AssistantText)
	}
}

func TestTurnEventSinkFailureAborts(t *testing.T) {
	session := &fakeSession{events: make(chan harness.AgentEvent, 1)}
	session.prompt = func(context.Context, string) error {
		session.events <- harness.AgentEvent{Type: "agent_settled", Kind: harness.AgentEventSettled, Data: json.RawMessage(`{"type":"agent_settled"}`)}
		return nil
	}
	_, err := harness.RunAgentTurn(context.Background(), session, "go", harness.TurnLimits{}, func(harness.Event) error { return context.Canceled })
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("err = %v", err)
	}
	if session.aborts != 1 {
		t.Fatalf("aborts = %d", session.aborts)
	}
}
