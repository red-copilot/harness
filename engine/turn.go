package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// ErrAgentUnavailable marks a session that cannot receive another turn.
const ReportFileName = "report.json"

var ErrAgentUnavailable = errors.New("agent session unavailable")

// RunAgentTurn sends one prompt and consumes normalized events until the
// adapter reports settlement. Existing callers keep fail-closed abort behavior.
func RunAgentTurn(ctx context.Context, session AgentSession, prompt string, limits TurnLimits, sink EventSink) (TurnResult, error) {
	return runAgentTurn(ctx, session, prompt, limits, sink, true)
}

func runAgentTurn(ctx context.Context, session AgentSession, prompt string, limits TurnLimits, sink EventSink, abortOnFailure bool) (TurnResult, error) {
	if session == nil {
		return TurnResult{}, errors.New("agent session is required")
	}
	if err := session.Prompt(ctx, prompt); err != nil {
		return TurnResult{}, err
	}
	abortIfNeeded := func() {
		if abortOnFailure {
			_ = abortSession(session)
		}
	}
	var result TurnResult
	toolCalls := 0
	activeTools := 0
	var timer *time.Timer
	var deadline <-chan time.Time
	stopTimer := func() {
		if timer != nil {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer = nil
		}
		deadline = nil
	}
	defer stopTimer()
	for {
		select {
		case <-ctx.Done():
			abortIfNeeded()
			return TurnResult{}, ctx.Err()
		case <-deadline:
			abortIfNeeded()
			return TurnResult{}, fmt.Errorf("tool timeout after %s", limits.ToolTimeout)
		case event, ok := <-session.Events():
			if !ok {
				return TurnResult{}, fmt.Errorf("%w: agent event stream closed before settlement", ErrAgentUnavailable)
			}
			if sink != nil {
				canonical, eventErr := NewEvent("", EventAgent, AgentEventPayload{NativeType: event.Type, Data: event.Data})
				if eventErr != nil {
					abortIfNeeded()
					return TurnResult{}, eventErr
				}
				canonical.Source = "agent"
				canonical.Raw = event.Data
				if err := sink(canonical); err != nil {
					abortIfNeeded()
					return TurnResult{}, err
				}
			}
			switch event.Kind {
			case AgentEventToolStart:
				if !event.WritesFile || !isReportPath(event.ToolPath) {
					toolCalls++
					if limits.MaxToolCalls > 0 && toolCalls > limits.MaxToolCalls {
						abortIfNeeded()
						return TurnResult{}, fmt.Errorf("tool call limit exceeded: %d > %d", toolCalls, limits.MaxToolCalls)
					}
				}
				activeTools++
				if limits.ToolTimeout > 0 && activeTools == 1 {
					stopTimer()
					timer = time.NewTimer(limits.ToolTimeout)
					deadline = timer.C
				}
			case AgentEventToolEnd:
				if activeTools > 0 {
					activeTools--
				}
				if activeTools == 0 {
					stopTimer()
				}
			case AgentEventMessage:
				if limits.MaxAssistantBytes > 0 && len(event.Text) > limits.MaxAssistantBytes {
					abortIfNeeded()
					return TurnResult{}, fmt.Errorf("assistant output limit exceeded: %d > %d", len(event.Text), limits.MaxAssistantBytes)
				}
				if event.Text != "" {
					result.AssistantText = event.Text
				}
			case AgentEventSettled:
				result.Usage = session.Usage()
				return result, nil
			case AgentEventError:
				abortIfNeeded()
				message := event.Text
				if message == "" {
					message = "agent reported an unrecoverable runtime error"
				}
				return TurnResult{}, fmt.Errorf("%w: %s", ErrAgentUnavailable, message)
			}
		}
	}
}

func abortSession(session AgentSession) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return session.Abort(ctx)
}

// isReportPath reports whether a write targets the one file the executor
// protocol requires. Writing the report is part of the contract, not a
// discretionary tool call, so it does not consume the tool budget.
func isReportPath(path string) bool {
	cleaned := filepath.Clean(path)
	return cleaned == ReportFileName || cleaned == filepath.Join(string(filepath.Separator), "workspace", ReportFileName)
}
