package harness_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

func TestReduceExplorationTurnRecordsObservationAndIsIdempotent(t *testing.T) {
	state := harness.NewExplorationState("run-1", "pi", harness.Task{ID: "task", Objective: "find the flag"}, harness.Target{ID: "web", Addresses: []string{"10.0.0.5:80"}})
	event := harness.Event{
		Seq:       7,
		RunID:     "run-1",
		SessionID: "pi",
		Source:    "agent",
		Type:      "turn_end",
		Data: json.RawMessage(`{
			"message":{"role":"assistant","content":[{"type":"toolCall","name":"bash"}]},
			"toolResults":[{"toolName":"bash","content":[{"type":"text","text":"port 80 is open"}],"isError":false}]
		}`),
	}

	next, err := harness.ReduceExploration(state, event)
	if err != nil {
		t.Fatalf("ReduceExploration: %v", err)
	}
	if next.Revision != 1 || next.LastEventSeq != 7 {
		t.Fatalf("revision/seq = %d/%d, want 1/7", next.Revision, next.LastEventSeq)
	}
	if len(next.Attempts) != 1 || !next.Attempts[0].ExecutionSucceeded {
		t.Fatalf("attempts = %#v", next.Attempts)
	}
	if got := strings.Join(next.Attempts[0].Tools, ","); got != "bash" {
		t.Fatalf("tools = %q, want bash", got)
	}
	if len(next.Observations) != 1 || !strings.Contains(next.Observations[0].Text, "port 80 is open") {
		t.Fatalf("observations = %#v", next.Observations)
	}

	duplicate, err := harness.ReduceExploration(next, event)
	if err != nil {
		t.Fatalf("duplicate ReduceExploration: %v", err)
	}
	if duplicate.Revision != next.Revision || len(duplicate.Attempts) != len(next.Attempts) || len(duplicate.Observations) != len(next.Observations) {
		t.Fatalf("duplicate changed state: before=%#v after=%#v", next, duplicate)
	}
}

func TestReduceExplorationRejectsOutOfOrderAndMalformedEvents(t *testing.T) {
	state := harness.NewExplorationState("run-1", "pi", harness.Task{ID: "task", Objective: "find the flag"}, harness.Target{})
	state.LastEventSeq = 9
	state.Revision = 1
	if _, err := harness.ReduceExploration(state, harness.Event{Seq: 8, RunID: "run-1", Type: "turn_end", Data: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("expected out-of-order event error")
	}
	if _, err := harness.ReduceExploration(state, harness.Event{Seq: 10, RunID: "run-1", Type: "turn_end", Data: json.RawMessage(`{`)}); err == nil {
		t.Fatal("expected malformed event error")
	}
}

func TestReduceExplorationDoesNotTreatAssistantTurnAsSuccess(t *testing.T) {
	state := harness.NewExplorationState("run-1", "pi", harness.Task{ID: "task", Objective: "find the flag"}, harness.Target{})
	event := harness.Event{
		Seq: 2, RunID: "run-1", SessionID: "pi", Type: "turn_end",
		Data: json.RawMessage(`{"message":{"role":"assistant","content":[{"type":"text","text":"I found the flag"}]},"toolResults":[]}`),
	}
	next, err := harness.ReduceExploration(state, event)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Attempts) != 1 || next.Attempts[0].ExecutionSucceeded {
		t.Fatalf("assistant prose was treated as execution success: %#v", next.Attempts)
	}
	if next.Attempts[0].Error != "turn produced no tool results" {
		t.Fatalf("attempt error = %q", next.Attempts[0].Error)
	}
	frontier := harness.ProjectExplorationFrontier(next, 1)
	if len(frontier.Items) != 1 || frontier.Items[0].Kind != harness.FrontierQuestion {
		t.Fatalf("frontier = %#v", frontier)
	}
}

func TestProjectExplorationFrontierPrioritizesUnverifiedAction(t *testing.T) {
	state := harness.NewExplorationState("run-1", "pi", harness.Task{ID: "task", Objective: "find the flag"}, harness.Target{ID: "web", Addresses: []string{"10.0.0.5:80"}})
	initial := harness.ProjectExplorationFrontier(state, 10)
	if len(initial.Items) == 0 || initial.Items[0].Kind != harness.FrontierCandidate || !strings.Contains(initial.Items[0].Text, "10.0.0.5:80") {
		t.Fatalf("initial frontier = %#v", initial)
	}

	event := harness.Event{
		Seq: 3, RunID: "run-1", SessionID: "pi", Type: "turn_end",
		Data: json.RawMessage(`{"message":{"role":"assistant","content":[]},"toolResults":[{"toolName":"ffuf","content":[{"type":"text","text":"request denied"}],"isError":true}]}`),
	}
	next, err := harness.ReduceExploration(state, event)
	if err != nil {
		t.Fatalf("ReduceExploration: %v", err)
	}
	frontier := harness.ProjectExplorationFrontier(next, 1)
	if len(frontier.Items) != 1 {
		t.Fatalf("frontier items = %#v", frontier.Items)
	}
	item := frontier.Items[0]
	if item.Kind != harness.FrontierQuestion || !strings.Contains(item.Text, "ffuf") {
		t.Fatalf("top item = %#v", item)
	}
	if item.Priority <= initial.Items[0].Priority {
		t.Fatalf("failure priority %d <= target priority %d", item.Priority, initial.Items[0].Priority)
	}
	if next.Items[0].Status != harness.FrontierDone {
		t.Fatalf("target item status = %q, want done after an action", next.Items[0].Status)
	}
}

func TestFormatExplorationContextKeepsFocusAndMarksObservationsUntrusted(t *testing.T) {
	state := harness.NewExplorationState("run-1", "pi", harness.Task{ID: "task", Objective: "find the flag"}, harness.Target{ID: "web", Addresses: []string{"10.0.0.5:80"}})
	event := harness.Event{
		Seq: 4, RunID: "run-1", SessionID: "pi", Type: "turn_end",
		Data: json.RawMessage(`{"message":{"role":"assistant","content":[]},"toolResults":[{"toolName":"curl","content":[{"type":"text","text":"HTTP 200 with application body"}],"isError":false}]}`),
	}
	state, err := harness.ReduceExploration(state, event)
	if err != nil {
		t.Fatal(err)
	}
	context := harness.BuildExplorationContext(state, 2)
	text := harness.FormatExplorationContext(context)
	for _, want := range []string{"Focus for the next action", "curl", "untrusted", "find the flag"} {
		if !strings.Contains(text, want) {
			t.Fatalf("context missing %q:\n%s", want, text)
		}
	}
}

func TestRunnerUpdatesStateThenSteersFrontierBeforeNextTurn(t *testing.T) {
	store := newMemoryStore()
	session := newExplorationSession(store)
	runner, err := harness.NewRunner(harness.Config{
		Store: store, WorkspaceRoot: t.TempDir(),
		DefaultFactory: &fakeFactory{newSession: func(req harness.SessionRequest) harness.AgentSession {
			session.runID = req.RunID
			return session
		}},
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	result, err := runner.Run(context.Background(), harness.RunSpec{
		Task:     harness.Task{ID: "explore", Objective: "find the flag"},
		Target:   harness.Target{ID: "web", Addresses: []string{"10.0.0.5:80"}},
		Agents:   []harness.AgentSpec{{ID: "pi"}},
		Verifier: fakeVerifier{verdict: harness.Verdict{Success: true}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != harness.StatusSucceeded {
		t.Fatalf("status = %q", result.Status)
	}
	select {
	case steer := <-session.steers:
		for _, want := range []string{"Exploration update", "Focus for the next action", "bash"} {
			if !strings.Contains(steer, want) {
				t.Fatalf("steer missing %q:\n%s", want, steer)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("runner did not steer exploration context")
	}

	events, err := store.ListEvents(context.Background(), result.RunID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var stateIndex, frontierIndex, finishedIndex int
	for i, event := range events {
		switch event.Type {
		case "exploration.state.updated":
			stateIndex = i + 1
		case "exploration.frontier.updated":
			frontierIndex = i + 1
		case "session.finished":
			finishedIndex = i + 1
		}
	}
	if stateIndex == 0 || frontierIndex == 0 || finishedIndex == 0 {
		t.Fatalf("missing control events: %#v", events)
	}
	if !(stateIndex < frontierIndex && frontierIndex < finishedIndex) {
		t.Fatalf("control event order state=%d frontier=%d finished=%d", stateIndex, frontierIndex, finishedIndex)
	}
	if session.steerSawState == 0 || session.steerSawFrontier == 0 || session.steerSawState > session.steerSawFrontier {
		t.Fatalf("steer event order state=%d frontier=%d", session.steerSawState, session.steerSawFrontier)
	}
}

func TestRunnerFailsClosedWhenTurnCannotBeProjected(t *testing.T) {
	store := newMemoryStore()
	session := &malformedExplorationSession{events: make(chan harness.AgentEvent, 1), done: make(chan struct{})}
	runner, err := harness.NewRunner(harness.Config{
		Store: store, WorkspaceRoot: t.TempDir(),
		DefaultFactory: &fakeFactory{newSession: func(harness.SessionRequest) harness.AgentSession {
			return session
		}},
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	_, err = runner.Run(context.Background(), harness.RunSpec{
		Task:     harness.Task{ID: "explore", Objective: "find the flag"},
		Agents:   []harness.AgentSpec{{ID: "pi"}},
		Verifier: fakeVerifier{verdict: harness.Verdict{Success: true}},
	})
	if err == nil || !strings.Contains(err.Error(), "reduce exploration") {
		t.Fatalf("expected fail-closed exploration error, got %v", err)
	}
	if session.steers != 0 {
		t.Fatalf("steer calls = %d, want 0", session.steers)
	}
}

type explorationSession struct {
	store       *memoryStore
	runID       string
	events      chan harness.AgentEvent
	steers      chan string
	steerSignal chan struct{}
	done        chan struct{}
	once        sync.Once

	steerSawState    int
	steerSawFrontier int
}

func newExplorationSession(store *memoryStore) *explorationSession {
	return &explorationSession{
		store: store, events: make(chan harness.AgentEvent, 8), steers: make(chan string, 1),
		steerSignal: make(chan struct{}, 1), done: make(chan struct{}),
	}
}

func (s *explorationSession) Prompt(context.Context, string) error {
	go func() {
		s.events <- harness.AgentEvent{Type: "turn_end", Data: json.RawMessage(`{"message":{"role":"assistant","content":[{"type":"toolCall","name":"bash"}]},"toolResults":[{"toolName":"bash","content":[{"type":"text","text":"port 80 is open"}],"isError":false}]}`)}
		<-s.steerSignal
		s.events <- harness.AgentEvent{Type: "agent_settled", Data: json.RawMessage(`{"aborted":false}`)}
		close(s.events)
		s.once.Do(func() { close(s.done) })
	}()
	return nil
}

func (s *explorationSession) Steer(ctx context.Context, message string) error {
	events, _ := s.store.ListEvents(ctx, s.runID, 0)
	for i, event := range events {
		switch event.Type {
		case "exploration.state.updated":
			s.steerSawState = i + 1
		case "exploration.frontier.updated":
			s.steerSawFrontier = i + 1
		}
	}
	s.steers <- message
	s.steerSignal <- struct{}{}
	return nil
}

func (s *explorationSession) Events() <-chan harness.AgentEvent { return s.events }
func (s *explorationSession) Wait(ctx context.Context) (harness.AgentResult, error) {
	select {
	case <-s.done:
		return harness.AgentResult{Status: harness.StatusSucceeded}, nil
	case <-ctx.Done():
		return harness.AgentResult{Status: harness.StatusCancelled}, ctx.Err()
	}
}
func (s *explorationSession) Abort(context.Context) error { return nil }
func (s *explorationSession) Close(context.Context) error {
	select {
	case <-s.done:
	default:
		s.once.Do(func() { close(s.done) })
	}
	return nil
}

type malformedExplorationSession struct {
	events chan harness.AgentEvent
	done   chan struct{}
	steers int
	once   sync.Once
}

func (s *malformedExplorationSession) Prompt(context.Context, string) error {
	s.events <- harness.AgentEvent{Type: "turn_end", Data: json.RawMessage(`{`)}
	close(s.events)
	return nil
}

func (s *malformedExplorationSession) Steer(context.Context, string) error {
	s.steers++
	return nil
}

func (s *malformedExplorationSession) Events() <-chan harness.AgentEvent { return s.events }
func (s *malformedExplorationSession) Wait(ctx context.Context) (harness.AgentResult, error) {
	select {
	case <-s.done:
		return harness.AgentResult{Status: harness.StatusSucceeded}, nil
	case <-ctx.Done():
		return harness.AgentResult{Status: harness.StatusCancelled}, ctx.Err()
	}
}
func (s *malformedExplorationSession) Abort(context.Context) error { return nil }
func (s *malformedExplorationSession) Close(context.Context) error {
	s.once.Do(func() { close(s.done) })
	return nil
}
