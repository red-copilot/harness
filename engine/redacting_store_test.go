package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type captureStore struct {
	Store
	commit   Commit
	event    Event
	finalize FinalizationRecord
}

func (s *captureStore) Commit(_ context.Context, commit Commit) ([]Event, error) {
	s.commit = commit
	return commit.Events, nil
}
func (s *captureStore) AppendEvent(_ context.Context, event Event) (Event, error) {
	s.event = event
	return event, nil
}
func (s *captureStore) Finalize(_ context.Context, record FinalizationRecord) (Event, error) {
	s.finalize = record
	return record.Event, nil
}

func TestRedactingStoreCoversBoardCommitAndFinalization(t *testing.T) {
	secret := "super-secret-token"
	inner := &captureStore{}
	store := newRedactingStore(inner)
	store.register("run-1", NewRedactor(secret))
	now := time.Now().UTC()
	_, err := store.Commit(context.Background(), Commit{
		RunID: "run-1", Revision: 1,
		Facts:        []Fact{{ID: "f", RunID: "run-1", TaskID: "task", Kind: FactNegative, Description: secret, CreatedAt: now}},
		Intents:      []Intent{{ID: "i", RunID: "run-1", Description: secret, CreatedAt: now}},
		Hints:        []Hint{{ID: "h", RunID: "run-1", Content: secret, CreatedAt: now}},
		Attempts:     []IntentAttempt{{ID: "a", RunID: "run-1", Conclusion: secret, StartedAt: now}},
		Observations: []Observation{{ID: "o", RunID: "run-1", Summary: secret, CreatedAt: now}},
		Artifacts:    []Artifact{{ID: "r", RunID: "run-1", Name: secret, CreatedAt: now}},
		Events:       []Event{{RunID: "run-1", Source: "test", Type: EventAgent, Time: now, Data: json.RawMessage(`{"text":"` + secret + `"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(inner.commit)
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("secret persisted in commit: %s", encoded)
	}
	final := FinalizationRecord{
		Run:     RunRecord{ID: "run-1", Error: secret, Result: json.RawMessage(`{"error":"` + secret + `"}`)},
		Verdict: &VerdictRecord{RunID: "run-1", Message: secret, Evidence: json.RawMessage(`{"token":"` + secret + `"}`)},
		Event:   Event{RunID: "run-1", Source: "test", Type: EventRunFinalized, Data: json.RawMessage(`{"error":"` + secret + `"}`)},
	}
	if _, err := store.Finalize(context.Background(), final); err != nil {
		t.Fatal(err)
	}
	encoded, _ = json.Marshal(inner.finalize)
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("secret persisted in finalization: %s", encoded)
	}
}
