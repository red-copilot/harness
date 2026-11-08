package engine

import "testing"

func eventTypes(events []Event) []EventType {
	out := make([]EventType, len(events))
	for i, event := range events {
		out[i] = event.Type
	}
	return out
}

func TestAttemptEventsDoNotReleaseConcludedIntent(t *testing.T) {
	engine := &blackboardEngine{controller: &runController{id: "run-1"}}
	intent := Intent{ID: "intent-1", RunID: "run-1", State: IntentConcluded}
	attempt := IntentAttempt{ID: "attempt-1", RunID: "run-1", IntentID: intent.ID, Phase: IntentAttemptConcluded}
	events, err := engine.attemptEvents(attempt, intent, EventAttemptConcluded, EventIntentConcluded)
	if err != nil {
		t.Fatal(err)
	}
	got := eventTypes(events)
	if len(got) != 2 || got[0] != EventAttemptConcluded || got[1] != EventIntentConcluded {
		t.Fatalf("events = %v, want attempt.concluded then intent.concluded", got)
	}
	for _, typ := range got {
		if typ == EventIntentReleased {
			t.Fatalf("concluded intent emitted released event: %v", got)
		}
	}
}

func TestAttemptEventsEmitSingleReleaseOnRetry(t *testing.T) {
	engine := &blackboardEngine{controller: &runController{id: "run-1"}}
	intent := Intent{ID: "intent-1", RunID: "run-1", State: IntentOpen}
	attempt := IntentAttempt{ID: "attempt-1", RunID: "run-1", IntentID: intent.ID, Phase: IntentAttemptFailed}
	events, err := engine.attemptEvents(attempt, intent, EventAttemptFailed, EventIntentReleased)
	if err != nil {
		t.Fatal(err)
	}
	got := eventTypes(events)
	if len(got) != 2 || got[0] != EventAttemptFailed || got[1] != EventIntentReleased {
		t.Fatalf("events = %v, want attempt.failed then intent.released", got)
	}
}

func TestAttemptEventsSkipIntentEventWhenCanceledWithoutIntent(t *testing.T) {
	engine := &blackboardEngine{controller: &runController{id: "run-1"}}
	attempt := IntentAttempt{ID: "attempt-1", RunID: "run-1", Phase: IntentAttemptCancelled}
	events, err := engine.attemptEvents(attempt, Intent{}, EventAttemptFailed, "")
	if err != nil {
		t.Fatal(err)
	}
	got := eventTypes(events)
	if len(got) != 1 || got[0] != EventAttemptFailed {
		t.Fatalf("events = %v, want only attempt.failed", got)
	}
}
