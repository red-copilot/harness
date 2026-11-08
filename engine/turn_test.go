package engine

import (
	"context"
	"strings"
	"testing"
)

type turnEventSession struct {
	events chan AgentEvent
}

func (s *turnEventSession) Prompt(context.Context, string) error { return nil }
func (s *turnEventSession) Reset(context.Context) error          { return nil }
func (s *turnEventSession) Events() <-chan AgentEvent            { return s.events }
func (s *turnEventSession) Logs() <-chan LogEvent                { return nil }
func (s *turnEventSession) Usage() Usage                         { return Usage{} }
func (s *turnEventSession) Abort(context.Context) error          { return nil }
func (s *turnEventSession) Close(context.Context) error          { return nil }

func TestRunAgentTurnRejectsOversizedEventBeforeSink(t *testing.T) {
	session := &turnEventSession{events: make(chan AgentEvent, 2)}
	session.events <- AgentEvent{Kind: AgentEventToolEnd, Data: []byte(strings.Repeat("x", 11))}
	session.events <- AgentEvent{Kind: AgentEventSettled}

	sinkCalls := 0
	_, err := runAgentTurn(context.Background(), session, "prompt", TurnLimits{MaxAssistantBytes: 10}, func(Event) error {
		sinkCalls++
		return nil
	}, true)
	if err == nil || !strings.Contains(err.Error(), "event limit") {
		t.Fatalf("err = %v, want event limit error", err)
	}
	if sinkCalls != 0 {
		t.Fatalf("sink calls = %d, want 0", sinkCalls)
	}
}

func TestRunAgentTurnReportOnlyRejectsInvestigationTools(t *testing.T) {
	session := &turnEventSession{events: make(chan AgentEvent, 2)}
	session.events <- AgentEvent{Kind: AgentEventToolStart, Tool: "bash", ToolPath: "scan.sh"}
	session.events <- AgentEvent{Kind: AgentEventSettled}

	_, err := runAgentTurn(context.Background(), session, "prompt", TurnLimits{ReportOnly: true}, nil, true)
	if err == nil || !strings.Contains(err.Error(), "report-only") {
		t.Fatalf("err = %v, want report-only tool rejection", err)
	}
}

func TestRunAgentTurnReportOnlyAllowsReportWrite(t *testing.T) {
	session := &turnEventSession{events: make(chan AgentEvent, 2)}
	session.events <- AgentEvent{Kind: AgentEventToolStart, Tool: "write", ToolPath: "/workspace/report.json", WritesFile: true}
	session.events <- AgentEvent{Kind: AgentEventSettled}

	if _, err := runAgentTurn(context.Background(), session, "prompt", TurnLimits{ReportOnly: true}, nil, true); err != nil {
		t.Fatalf("report write rejected: %v", err)
	}
}
