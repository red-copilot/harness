package harness_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/red-copilot/harness"
)

func toolEventSession(workspace, report string) *fakeSession {
	s := &fakeSession{events: make(chan harness.AgentEvent, 8)}
	s.prompt = func(ctx context.Context, _ string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		s.events <- harness.AgentEvent{Type: "tool_start", Kind: harness.AgentEventToolStart, Tool: "shell", ToolPath: "/workspace/scan"}
		s.events <- harness.AgentEvent{Type: "tool_end", Kind: harness.AgentEventToolEnd, Tool: "shell", ToolPath: "/workspace/scan"}
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(workspace, "evidence.txt"), []byte("no signal"), 0o600); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(workspace, "report.json"), []byte(report), 0o600); err != nil {
			return err
		}
		s.events <- harness.AgentEvent{Type: "message", Kind: harness.AgentEventMessage, Text: "done"}
		s.events <- harness.AgentEvent{Kind: harness.AgentEventSettled}
		return nil
	}
	return s
}

func TestExecutorTurnEventsAreCanonical(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.Verifier = fakeVerifier{verdict: harness.Verdict{Success: false}}
	spec.ReasonStrategy = oneIntentReasonStrategy{}
	spec.Reasoner.Runtime = &fakeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		return promptReportSession(req.WorkspaceDir, `{"board_revision":1,"intents":[{"from":["origin"],"description":"probe"}]}`)
	}}
	spec.Executor.Runtime = &fakeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		return toolEventSession(req.WorkspaceDir, evidenceBackedReport("negative", "no signal"))
	}}

	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	events, err := store.ListEvents(context.Background(), result.RunID, 0)
	if err != nil {
		t.Fatal(err)
	}
	executorEvents := 0
	sawTool := false
	for _, event := range events {
		if event.Type != harness.EventAgent || event.SessionID != "executor" {
			continue
		}
		executorEvents++
		if strings.Contains(string(event.Data), "tool_start") || strings.Contains(string(event.Raw), "tool_start") {
			sawTool = true
		}
	}
	if executorEvents < 2 || !sawTool {
		t.Fatalf("executor canonical events = %d sawTool=%v, want >=2 including a tool event", executorEvents, sawTool)
	}
}
