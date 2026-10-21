package exploration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/red-copilot/harness"
	"path/filepath"
	"strings"
	"time"
)

// EventSink persists raw session events without letting the explorer consume
// the only copy of the audit stream.
type EventSink func(harness.AgentEvent) error

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
func RunTurn(ctx context.Context, session harness.AgentSession, prompt string, sink EventSink) (TurnOutcome, error) {
	return RunTurnWithLimits(ctx, session, prompt, sink, TurnLimits{})
}

// RunTurnWithLimits aborts a session as soon as a hard tool or tool-time limit
// is crossed. A final accounting pass is only a fallback for sessions that do
// not emit live tool_execution_start events.
func RunTurnWithLimits(
	ctx context.Context,
	session harness.AgentSession,
	prompt string,
	sink EventSink,
	limits TurnLimits,
) (TurnOutcome, error) {
	if session == nil {
		return TurnOutcome{}, errors.New("agent session is required")
	}
	if err := session.Prompt(ctx, prompt); err != nil {
		if ctx.Err() != nil {
			_ = abortAndDrain(session, sink)
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
			_ = abortAndDrain(session, sink)
			return TurnOutcome{}, ctx.Err()
		case <-toolDeadline:
			_ = abortAndDrain(session, sink)
			return TurnOutcome{}, fmt.Errorf("tool timeout after %s", limits.ToolTimeout)
		case event, ok := <-session.Events():
			if !ok {
				if outcome.AssistantText == "" {
					return TurnOutcome{}, errors.New("agent event stream closed before settlement")
				}
				return TurnOutcome{}, errors.New("agent event stream closed before agent_settled")
			}
			if sink != nil {
				if err := sink(event); err != nil {
					return TurnOutcome{}, err
				}
			}
			switch event.Type {
			case "tool_execution_start":
				if !isReportWrite(event.Data) {
					liveToolCalls++
					if limits.MaxToolCalls > 0 && liveToolCalls > limits.MaxToolCalls {
						_ = abortAndDrain(session, sink)
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
					_ = abortAndDrain(session, sink)
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

func abortAndDrain(session harness.AgentSession, sink EventSink) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = session.Abort(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-session.Events():
			if !ok {
				return nil
			}
			if sink != nil {
				if err := sink(event); err != nil {
					return err
				}
			}
			if event.Type == "agent_settled" {
				return nil
			}
		}
	}
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
