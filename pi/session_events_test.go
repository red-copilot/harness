package pi

import (
	"encoding/json"
	"testing"

	"github.com/red-copilot/harness"
)

func TestNormalizeEventMapsPiWireRecords(t *testing.T) {
	tests := []struct {
		name       string
		recordType string
		line       string
		kind       harness.AgentEventKind
		text       string
		tools      []string
		tool       string
		toolPath   string
		writesFile bool
	}{
		{
			name:       "turn_end carries assistant text and tool calls",
			recordType: "turn_end",
			line:       `{"type":"turn_end","message":{"content":[{"type":"text","text":"hello\nworld"},{"type":"toolCall","name":"bash"}]}}`,
			kind:       harness.AgentEventMessage,
			text:       "hello\nworld",
			tools:      []string{"bash"},
		},
		{
			name:       "message_end falls back to tool results",
			recordType: "message_end",
			line:       `{"type":"message_end","message":{"content":[]},"toolResults":[{"toolName":"bash"}]}`,
			kind:       harness.AgentEventMessage,
			tools:      []string{"bash"},
		},
		{
			name:       "write tool is marked as a file write",
			recordType: "tool_execution_start",
			line:       `{"type":"tool_execution_start","toolName":"write","args":{"path":"/workspace/report.json"}}`,
			kind:       harness.AgentEventToolStart,
			tool:       "write",
			toolPath:   "/workspace/report.json",
			writesFile: true,
		},
		{
			name:       "non-write tool is not a file write",
			recordType: "tool_execution_start",
			line:       `{"type":"tool_execution_start","toolName":"bash","args":{"command":"ls"}}`,
			kind:       harness.AgentEventToolStart,
			tool:       "bash",
		},
		{
			name:       "tool completion closes the tool window",
			recordType: "tool_execution_end",
			line:       `{"type":"tool_execution_end","toolName":"bash"}`,
			kind:       harness.AgentEventToolEnd,
		},
		{
			name:       "settled is completion",
			recordType: "agent_settled",
			line:       `{"type":"agent_settled"}`,
			kind:       harness.AgentEventSettled,
		},
		{
			name:       "agent_end is not completion",
			recordType: "agent_end",
			line:       `{"type":"agent_end"}`,
			kind:       harness.AgentEventEnd,
		},
		{
			name:       "unknown records stay audit-only",
			recordType: "message_start",
			line:       `{"type":"message_start","message":{"role":"assistant"}}`,
			kind:       "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := normalizeEvent(test.recordType, []byte(test.line))
			if event.Kind != test.kind {
				t.Fatalf("kind = %q, want %q", event.Kind, test.kind)
			}
			if event.Type != test.recordType {
				t.Fatalf("type = %q, want %q", event.Type, test.recordType)
			}
			if string(event.Data) != test.line {
				t.Fatalf("data = %s, want the verbatim record", event.Data)
			}
			if event.Text != test.text {
				t.Fatalf("text = %q, want %q", event.Text, test.text)
			}
			if got := stringsOf(event.Tools); got != stringsOf(test.tools) {
				t.Fatalf("tools = %v, want %v", event.Tools, test.tools)
			}
			if event.Tool != test.tool || event.ToolPath != test.toolPath || event.WritesFile != test.writesFile {
				t.Fatalf("tool = %q path = %q writes = %v, want %q %q %v", event.Tool, event.ToolPath, event.WritesFile, test.tool, test.toolPath, test.writesFile)
			}
		})
	}
}

func TestNormalizeEventKeepsMalformedRecordAuditOnly(t *testing.T) {
	event := normalizeEvent("turn_end", []byte(`not json`))
	if event.Kind != harness.AgentEventMessage || event.Text != "" || len(event.Tools) != 0 {
		t.Fatalf("malformed record mapped to %+v", event)
	}
	if string(event.Data) != "not json" {
		t.Fatalf("data = %q, want the verbatim record", event.Data)
	}
}

func stringsOf(values []string) string {
	data, _ := json.Marshal(values)
	return string(data)
}
