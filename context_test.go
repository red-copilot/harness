package harness_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

func TestKnowledgeDigestHonorsCountAndByteLimits(t *testing.T) {
	task := harness.Task{ID: "task-1", Objective: "objective"}
	target := harness.Target{ID: "target-1", Isolation: harness.IsolationCallerManaged}
	now := time.Now().UTC()
	items := make([]harness.Knowledge, 0, 20)
	for i := 0; i < 20; i++ {
		items = append(items, harness.Knowledge{ID: string(rune('a' + i)), TaskID: task.ID, RunID: "run-1", Topic: "topic", Kind: "adapter.kind", Statement: strings.Repeat("界", 300), Scope: harness.KnowledgeScopeTask, Trust: harness.KnowledgeObserved, Reuse: harness.ReuseStable, ValidFrom: now.Add(time.Duration(i) * time.Second)})
	}
	request, dropped := harness.BuildPlannerContext(task, target, nil, items, harness.Completion{}, nil)
	if len(request.Knowledge) == 0 || len(request.Knowledge) > 8 {
		t.Fatalf("knowledge count = %d", len(request.Knowledge))
	}
	total := 0
	for _, item := range request.Knowledge {
		if len(item.Statement) > 200 {
			t.Fatalf("statement bytes = %d", len(item.Statement))
		}
		total += len(item.Statement)
	}
	if total > 2048 {
		t.Fatalf("digest bytes = %d", total)
	}
	if dropped == 0 {
		t.Fatal("dropped count should be non-zero")
	}
}

func TestKnowledgeDigestOrdersDeterministically(t *testing.T) {
	now := time.Now().UTC()
	items := []harness.Knowledge{
		{ID: "z-observed", TaskID: "task-1", Statement: "observed", Trust: harness.KnowledgeObserved, ValidFrom: now},
		{ID: "b-corrob", TaskID: "task-1", Statement: "corrob-b", Trust: harness.KnowledgeCorroborated, ValidFrom: now.Add(-time.Hour)},
		{ID: "a-corrob", TaskID: "task-1", Statement: "corrob-a", Trust: harness.KnowledgeCorroborated, ValidFrom: now.Add(-time.Hour)},
		{ID: "c-contested", TaskID: "task-1", Statement: "contested", Trust: harness.KnowledgeContested, ValidFrom: now},
	}
	req, _ := harness.BuildPlannerContext(harness.Task{ID: "task-1", Objective: "x"}, harness.Target{ID: "target-1", Isolation: harness.IsolationCallerManaged}, nil, items, harness.Completion{}, nil)
	got := []string{}
	for _, item := range req.Knowledge {
		got = append(got, item.ID)
	}
	want := []string{"a-corrob", "b-corrob", "z-observed", "c-contested"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestKnowledgeDigestExcludesRefutedExpiredAndWrongScope(t *testing.T) {
	now := time.Now().UTC()
	expired := now.Add(-time.Minute)
	items := []harness.Knowledge{
		{ID: "keep", TaskID: "task-1", Target: "target-1", Statement: "keep", Scope: harness.KnowledgeScopeTarget, Trust: harness.KnowledgeObserved, ValidFrom: now},
		{ID: "refuted", TaskID: "task-1", Target: "target-1", Statement: "refuted", Scope: harness.KnowledgeScopeTarget, Trust: harness.KnowledgeRefuted, ValidFrom: now},
		{ID: "expired", TaskID: "task-1", Target: "target-1", Statement: "expired", Scope: harness.KnowledgeScopeTarget, Trust: harness.KnowledgeObserved, ValidFrom: now, ExpiresAt: &expired},
		{ID: "wrong-target", TaskID: "task-1", Target: "other", Statement: "wrong", Scope: harness.KnowledgeScopeTarget, Trust: harness.KnowledgeObserved, ValidFrom: now},
	}
	req, _ := harness.BuildPlannerContext(harness.Task{ID: "task-1", Objective: "x"}, harness.Target{ID: "target-1", Isolation: harness.IsolationCallerManaged}, nil, items, harness.Completion{}, nil)
	if len(req.Knowledge) != 1 || req.Knowledge[0].ID != "keep" {
		t.Fatalf("knowledge = %+v", req.Knowledge)
	}
}

func TestPlannerAndExecutorReceiveOnlyBoundedRecentReports(t *testing.T) {
	now := time.Now().UTC()
	branches := []harness.Branch{{ID: "b1", Topic: "one", Status: harness.BranchBlocked, CreatedAt: now}, {ID: "b2", Topic: "two", Status: harness.BranchBlocked, CreatedAt: now}, {ID: "b3", Topic: "three", Status: harness.BranchBlocked, CreatedAt: now}}
	attempts := []harness.Attempt{{ID: "a1", BranchID: "b1", Status: harness.AttemptFailed, Outcome: harness.AttemptFailure}, {ID: "a2", BranchID: "b2", Status: harness.AttemptFailed, Outcome: harness.AttemptFailure}, {ID: "a3", BranchID: "b3", Status: harness.AttemptFailed, Outcome: harness.AttemptFailure}}
	planner, _ := harness.BuildPlannerContext(harness.Task{ID: "task-1", Objective: "x"}, harness.Target{ID: "target-1", Isolation: harness.IsolationCallerManaged}, branches, nil, harness.Completion{}, attempts)
	if len(planner.LastReports) > 2 {
		t.Fatalf("planner reports = %d", len(planner.LastReports))
	}
	executor, _ := harness.BuildExecutorContext(harness.Task{ID: "task-1", Objective: "x"}, harness.Target{ID: "target-1", Isolation: harness.IsolationCallerManaged}, branches[0], nil, harness.AttemptBudget{MaxAttempts: 3, MaxToolCalls: 20, MaxWallTime: time.Minute}, t.TempDir())
	if executor.Branch.ID != "b1" {
		t.Fatalf("executor branch = %q", executor.Branch.ID)
	}
}

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
