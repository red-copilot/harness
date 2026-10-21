package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	maxExplorationAttempts     = 64
	maxExplorationObservations = 32
	maxExplorationText         = 1024
)

// ExplorationState is the deterministic projection of controller-visible
// actions for one agent session. It is not an authority for task success.
type ExplorationState struct {
	RunID        string                   `json:"run_id"`
	SessionID    string                   `json:"session_id"`
	Revision     int64                    `json:"revision"`
	LastEventSeq int64                    `json:"last_event_seq"`
	Task         Task                     `json:"task"`
	Target       Target                   `json:"target,omitempty"`
	Attempts     []ActionAttempt          `json:"attempts,omitempty"`
	Observations []ExplorationObservation `json:"observations,omitempty"`
	Items        []FrontierItem           `json:"items,omitempty"`
}

// ActionAttempt records one agent turn. Success here only means the turn
// produced at least one non-error tool result; it never means objective success.
type ActionAttempt struct {
	EventSeq           int64    `json:"event_seq"`
	Tools              []string `json:"tools,omitempty"`
	ExecutionSucceeded bool     `json:"execution_succeeded"`
	Error              string   `json:"error,omitempty"`
	Observations       int      `json:"observations"`
}

// ExplorationObservation is untrusted observation text captured from a tool
// result. Trust is established only by a Verifier or backend completion.
type ExplorationObservation struct {
	EventSeq int64  `json:"event_seq"`
	Tool     string `json:"tool"`
	Text     string `json:"text,omitempty"`
	Error    bool   `json:"error,omitempty"`
}

// NewExplorationState creates the initial state for one agent session.
func NewExplorationState(runID, sessionID string, task Task, target Target) ExplorationState {
	return ExplorationState{
		RunID: runID, SessionID: sessionID, Task: task, Target: target,
		Items: seedFrontierItems(task, target),
	}
}

// ReduceExploration applies one Pi turn_end event to the current state. It is
// pure, idempotent for an already-applied sequence, and rejects sequence
// regression in the event stream.
func ReduceExploration(previous ExplorationState, event Event) (ExplorationState, error) {
	if event.Seq <= 0 {
		return ExplorationState{}, errors.New("exploration event requires a positive sequence")
	}
	if previous.RunID != "" && event.RunID != previous.RunID {
		return ExplorationState{}, fmt.Errorf("exploration run_id %q does not match %q", event.RunID, previous.RunID)
	}
	if previous.SessionID != "" && event.SessionID != previous.SessionID {
		return ExplorationState{}, fmt.Errorf("exploration session_id %q does not match %q", event.SessionID, previous.SessionID)
	}
	if event.Type != "turn_end" {
		return ExplorationState{}, fmt.Errorf("exploration cannot reduce event type %q", event.Type)
	}
	if event.Seq == previous.LastEventSeq {
		return previous, nil
	}
	if event.Seq < previous.LastEventSeq {
		return ExplorationState{}, fmt.Errorf("exploration event %d is older than last event %d", event.Seq, previous.LastEventSeq)
	}

	var payload turnEndPayload
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return ExplorationState{}, fmt.Errorf("parse turn_end: %w", err)
	}

	tools := resultTools(payload.ToolResults)
	if len(tools) == 0 {
		tools = messageTools(payload.Message)
	}
	if len(tools) == 0 {
		tools = []string{"agent"}
	}

	attempt := ActionAttempt{EventSeq: event.Seq, Tools: uniqueStrings(tools), Observations: len(payload.ToolResults)}
	var errorParts []string
	for _, result := range payload.ToolResults {
		text := resultText(result.Content)
		observation := ExplorationObservation{
			EventSeq: event.Seq, Tool: result.ToolName, Text: truncateExplorationText(text), Error: result.IsError,
		}
		previous.Observations = append(previous.Observations, observation)
		if result.IsError {
			if text != "" {
				errorParts = append(errorParts, result.ToolName+": "+truncateExplorationText(text))
			} else {
				errorParts = append(errorParts, result.ToolName+": tool reported an error")
			}
		}
	}
	attempt.ExecutionSucceeded = len(payload.ToolResults) > 0 && len(errorParts) == 0
	if len(payload.ToolResults) == 0 {
		attempt.Error = "turn produced no tool results"
	} else if len(errorParts) > 0 {
		attempt.Error = strings.Join(errorParts, "; ")
	}

	previous.Attempts = append(previous.Attempts, attempt)
	for i := range previous.Items {
		if previous.Items[i].Status == FrontierOpen && previous.Items[i].ID != "goal:next-evidence" {
			previous.Items[i].Status = FrontierDone
		}
	}
	previous.Items = append(previous.Items, frontierItemForAttempt(attempt))
	previous.Items = trimFrontierItems(previous.Items, 64)
	previous.Attempts = trimAttempts(previous.Attempts, maxExplorationAttempts)
	previous.Observations = trimObservations(previous.Observations, maxExplorationObservations)
	previous.Revision++
	previous.LastEventSeq = event.Seq
	return previous, nil
}

type turnEndPayload struct {
	Message     json.RawMessage  `json:"message"`
	ToolResults []turnToolResult `json:"toolResults"`
}

type turnMessage struct {
	Content []turnMessageContent `json:"content"`
}

type turnMessageContent struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type turnToolResult struct {
	ToolName string            `json:"toolName"`
	Content  []turnContentPart `json:"content"`
	IsError  bool              `json:"isError"`
}

type turnContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func messageTools(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var message turnMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		return nil
	}
	var tools []string
	for _, part := range message.Content {
		if part.Type == "toolCall" && part.Name != "" {
			tools = append(tools, part.Name)
		}
	}
	return tools
}

func resultTools(results []turnToolResult) []string {
	var tools []string
	for _, result := range results {
		if result.ToolName != "" {
			tools = append(tools, result.ToolName)
		}
	}
	return tools
}

func resultText(content []turnContentPart) string {
	var parts []string
	for _, part := range content {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func truncateExplorationText(text string) string {
	if len(text) <= maxExplorationText {
		return text
	}
	return text[:maxExplorationText] + "\n[truncated]"
}

func trimAttempts(values []ActionAttempt, limit int) []ActionAttempt {
	if len(values) <= limit {
		return values
	}
	return append([]ActionAttempt(nil), values[len(values)-limit:]...)
}

func trimObservations(values []ExplorationObservation, limit int) []ExplorationObservation {
	if len(values) <= limit {
		return values
	}
	return append([]ExplorationObservation(nil), values[len(values)-limit:]...)
}

func seedFrontierItems(task Task, target Target) []FrontierItem {
	items := targetCandidates(target)
	if task.Objective != "" {
		items = append(items, FrontierItem{
			ID: "goal:next-evidence", Kind: FrontierQuestion, Status: FrontierOpen,
			Text:     "Choose the next concrete evidence-producing step toward: " + task.Objective,
			Priority: 10,
		})
	}
	return items
}

func frontierItemForAttempt(attempt ActionAttempt) FrontierItem {
	tools := strings.Join(attempt.Tools, ", ")
	if tools == "" {
		tools = "agent"
	}
	if !attempt.ExecutionSucceeded {
		text := fmt.Sprintf("Determine whether the failed %s attempt was transient, misdirected, or contradicted by evidence.", tools)
		if attempt.Error != "" {
			text += " Failure: " + attempt.Error
		}
		return FrontierItem{
			ID: fmt.Sprintf("question:failed:%d", attempt.EventSeq), Kind: FrontierQuestion, Status: FrontierOpen,
			Text: text, Priority: 100, SourceSeq: attempt.EventSeq,
		}
	}
	return FrontierItem{
		ID: fmt.Sprintf("question:verify:%d", attempt.EventSeq), Kind: FrontierQuestion, Status: FrontierOpen,
		Text:     fmt.Sprintf("Independently verify the observation produced by %s before treating it as progress.", tools),
		Priority: 90, SourceSeq: attempt.EventSeq,
	}
}

func trimFrontierItems(values []FrontierItem, limit int) []FrontierItem {
	if len(values) <= limit {
		return values
	}
	return append([]FrontierItem(nil), values[len(values)-limit:]...)
}
