package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type scriptedExploreSession struct {
	mu      sync.Mutex
	prompts []string
	events  chan AgentEvent
	calls   int
	aborts  int
}

func (s *scriptedExploreSession) Prompt(ctx context.Context, prompt string) error {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.prompts = append(s.prompts, prompt)
	s.mu.Unlock()
	if call == 1 {
		<-ctx.Done()
		return ctx.Err()
	}
	s.events <- AgentEvent{Kind: AgentEventMessage, Text: `{"intent_id":"intent-1","phase":"concluded","fact":{"kind":"negative","description":"nothing confirmed"}}`}
	s.events <- AgentEvent{Kind: AgentEventSettled}
	return nil
}
func (s *scriptedExploreSession) Steer(context.Context, string) error { return nil }
func (s *scriptedExploreSession) Reset(context.Context) error         { return nil }
func (s *scriptedExploreSession) Events() <-chan AgentEvent           { return s.events }
func (s *scriptedExploreSession) Logs() <-chan LogEvent               { return nil }
func (s *scriptedExploreSession) Usage() Usage                        { return Usage{} }
func (s *scriptedExploreSession) Abort(context.Context) error {
	s.mu.Lock()
	s.aborts++
	s.mu.Unlock()
	return nil
}
func (s *scriptedExploreSession) Close(context.Context) error { return nil }

type exitExploreSession struct {
	events chan AgentEvent
	calls  int
}

func (s *exitExploreSession) Prompt(context.Context, string) error {
	s.calls++
	return errors.New("pi exited before writing command")
}
func (s *exitExploreSession) Steer(context.Context, string) error { return nil }
func (s *exitExploreSession) Reset(context.Context) error         { return nil }
func (s *exitExploreSession) Events() <-chan AgentEvent           { return s.events }
func (s *exitExploreSession) Logs() <-chan LogEvent               { return nil }
func (s *exitExploreSession) Usage() Usage                        { return Usage{} }
func (s *exitExploreSession) Abort(context.Context) error         { return nil }
func (s *exitExploreSession) Close(context.Context) error         { return nil }

type errorEventSession struct {
	events chan AgentEvent
	calls  int
}

func (s *errorEventSession) Prompt(context.Context, string) error {
	s.calls++
	s.events <- AgentEvent{Kind: AgentEventError, Text: "pi process exited"}
	return nil
}
func (s *errorEventSession) Steer(context.Context, string) error { return nil }
func (s *errorEventSession) Reset(context.Context) error         { return nil }
func (s *errorEventSession) Events() <-chan AgentEvent           { return s.events }
func (s *errorEventSession) Logs() <-chan LogEvent               { return nil }
func (s *errorEventSession) Usage() Usage                        { return Usage{} }
func (s *errorEventSession) Abort(context.Context) error         { return nil }
func (s *errorEventSession) Close(context.Context) error         { return nil }

type defaultExploreTestStrategy struct{}

func (defaultExploreTestStrategy) ExecutePrompt(ExploreRequest) (string, error) {
	return "execute", nil
}
func (defaultExploreTestStrategy) ConcludePrompt(ExploreRequest) (string, error) {
	return "conclude", nil
}
func (defaultExploreTestStrategy) Parse(_ ExploreRequest, turn TurnResult) (ExploreReport, error) {
	var report ExploreReport
	if err := json.Unmarshal([]byte(strings.TrimSpace(turn.AssistantText)), &report); err != nil {
		return ExploreReport{}, err
	}
	if report.IntentID == "" {
		report.IntentID = "intent-1"
	}
	if report.Phase == "" {
		report.Phase = IntentAttemptConcluded
	}
	return report, nil
}

func testExploreRequest() ExploreRequest {
	return ExploreRequest{Intent: Intent{ID: "intent-1", Description: "probe"}, Task: Task{ID: "task-1", Objective: "test"}}
}

func TestExploreParseRequiresFindingEvidence(t *testing.T) {
	strategy := defaultExploreStrategy{}
	for _, body := range []string{
		`{"intent_id":"intent-1","phase":"concluded","fact":{"kind":"finding","description":"done"}}`,
		`{"intent_id":"intent-1","phase":"concluded","fact":{"kind":"goal","description":"done"}}`,
	} {
		if _, err := strategy.Parse(testExploreRequest(), TurnResult{AssistantText: body}); err == nil {
			t.Fatalf("invalid explore report accepted: %s", body)
		}
	}
	if _, err := strategy.Parse(testExploreRequest(), TurnResult{AssistantText: `{"intent_id":"intent-1","phase":"concluded","fact":{"kind":"negative","description":"nothing"}}`}); err != nil {
		t.Fatalf("negative report rejected: %v", err)
	}
}

func TestConcludeRunsAfterExecuteTimeout(t *testing.T) {
	session := &scriptedExploreSession{events: make(chan AgentEvent, 4)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	report, phase, err := runExploreAttempt(ctx, session, testExploreRequest(), TurnLimits{}, 100*time.Millisecond, nil, defaultExploreTestStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	if phase != "conclude" || report.Fact == nil || report.Fact.Kind != FactNegative {
		t.Fatalf("phase=%q report=%+v", phase, report)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if len(session.prompts) != 2 || session.prompts[1] != "conclude" {
		t.Fatalf("prompts = %v", session.prompts)
	}
}

func TestConcludeDoesNotRunAfterProcessExit(t *testing.T) {
	session := &exitExploreSession{events: make(chan AgentEvent)}
	_, _, err := runExploreAttempt(context.Background(), session, testExploreRequest(), TurnLimits{}, 100*time.Millisecond, nil, defaultExploreTestStrategy{})
	if err == nil {
		t.Fatal("expected process failure")
	}
	if session.calls != 1 {
		t.Fatalf("prompt calls = %d, want 1", session.calls)
	}
}

func TestConcludeDoesNotRunAfterAgentErrorEvent(t *testing.T) {
	session := &errorEventSession{events: make(chan AgentEvent, 1)}
	_, _, err := runExploreAttempt(context.Background(), session, testExploreRequest(), TurnLimits{}, 100*time.Millisecond, nil, defaultExploreTestStrategy{})
	if err == nil || !errors.Is(err, ErrAgentUnavailable) {
		t.Fatalf("err = %v, want agent unavailable", err)
	}
	if session.calls != 1 {
		t.Fatalf("prompt calls = %d, want 1", session.calls)
	}
}

func TestConcludeCannotExceedItsTimeout(t *testing.T) {
	session := &scriptedExploreSession{events: make(chan AgentEvent, 4)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, phase, err := runExploreAttempt(ctx, session, testExploreRequest(), TurnLimits{}, 10*time.Millisecond, nil, blockingExploreStrategy{})
	if err == nil || phase != "conclude" {
		t.Fatalf("phase=%q err=%v", phase, err)
	}
}

type blockingExploreStrategy struct{}

func (blockingExploreStrategy) ExecutePrompt(ExploreRequest) (string, error)  { return "execute", nil }
func (blockingExploreStrategy) ConcludePrompt(ExploreRequest) (string, error) { return "conclude", nil }
func (blockingExploreStrategy) Parse(ExploreRequest, TurnResult) (ExploreReport, error) {
	return ExploreReport{}, errors.New("not used")
}

type closedEventsSession struct {
	events chan AgentEvent
	calls  int
}

func (s *closedEventsSession) Prompt(context.Context, string) error { s.calls++; return nil }
func (s *closedEventsSession) Steer(context.Context, string) error  { return nil }
func (s *closedEventsSession) Reset(context.Context) error          { return nil }
func (s *closedEventsSession) Events() <-chan AgentEvent            { return s.events }
func (s *closedEventsSession) Logs() <-chan LogEvent                { return nil }
func (s *closedEventsSession) Usage() Usage                         { return Usage{} }
func (s *closedEventsSession) Abort(context.Context) error          { return nil }
func (s *closedEventsSession) Close(context.Context) error          { return nil }

func TestConcludeDoesNotRunAfterEventStreamClose(t *testing.T) {
	events := make(chan AgentEvent)
	close(events)
	session := &closedEventsSession{events: events}
	_, _, err := runExploreAttempt(context.Background(), session, testExploreRequest(), TurnLimits{}, 100*time.Millisecond, nil, defaultExploreTestStrategy{})
	if !errors.Is(err, ErrAgentUnavailable) {
		t.Fatalf("err = %v, want ErrAgentUnavailable", err)
	}
	if session.calls != 1 {
		t.Fatalf("prompt calls = %d, want 1", session.calls)
	}
}

func TestExploreParserExtractsEmbeddedJSON(t *testing.T) {
	req := testExploreRequest()
	turn := TurnResult{AssistantText: "Summary first.\n{\"intent_id\":\"intent-1\",\"phase\":\"concluded\",\"fact\":{\"kind\":\"negative\",\"description\":\"nothing\"}}\nDone."}
	report, err := (defaultExploreStrategy{}).Parse(req, turn)
	if err != nil {
		t.Fatal(err)
	}
	if report.Fact == nil || report.Fact.Kind != FactNegative {
		t.Fatalf("report = %+v", report)
	}
}

func TestExploreParserAcceptsPromptIntentPlaceholder(t *testing.T) {
	req := testExploreRequest()
	turn := TurnResult{AssistantText: `{"intent_id":"INTENT_ID","phase":"concluded","fact":{"kind":"negative","description":"nothing"}}`}
	report, err := (defaultExploreStrategy{}).Parse(req, turn)
	if err != nil {
		t.Fatal(err)
	}
	if report.IntentID != req.Intent.ID {
		t.Fatalf("intent_id = %q, want %q", report.IntentID, req.Intent.ID)
	}
}
