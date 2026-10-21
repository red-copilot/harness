package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// spyStore records every write argument. It embeds a nil Store because the
// tests only exercise write paths.
type spyStore struct {
	Store
	runs         []RunRecord
	events       []Event
	logs         []LogRecord
	verdicts     []VerdictRecord
	branches     []Branch
	attempts     []Attempt
	observations []Observation
	artifacts    []Artifact
	knowledge    []Knowledge
}

func (s *spyStore) CreateRun(_ context.Context, record RunRecord) error {
	s.runs = append(s.runs, record)
	return nil
}
func (s *spyStore) UpdateRun(_ context.Context, record RunRecord) error {
	s.runs = append(s.runs, record)
	return nil
}
func (s *spyStore) AppendEvent(_ context.Context, event Event) (Event, error) {
	s.events = append(s.events, event)
	return event, nil
}
func (s *spyStore) AppendLog(_ context.Context, record LogRecord) (LogRecord, error) {
	s.logs = append(s.logs, record)
	return record, nil
}
func (s *spyStore) SaveVerdict(_ context.Context, record VerdictRecord) error {
	s.verdicts = append(s.verdicts, record)
	return nil
}
func (s *spyStore) Finalize(_ context.Context, record FinalizationRecord) (Event, error) {
	s.runs = append(s.runs, record.Run)
	if record.Verdict != nil {
		s.verdicts = append(s.verdicts, *record.Verdict)
	}
	s.events = append(s.events, record.Event)
	return record.Event, nil
}
func (s *spyStore) SaveBranch(_ context.Context, branch Branch) error {
	s.branches = append(s.branches, branch)
	return nil
}
func (s *spyStore) SaveAttempt(_ context.Context, attempt Attempt) error {
	s.attempts = append(s.attempts, attempt)
	return nil
}
func (s *spyStore) SaveObservation(_ context.Context, observation Observation) error {
	s.observations = append(s.observations, observation)
	return nil
}
func (s *spyStore) SaveArtifact(_ context.Context, artifact Artifact) error {
	s.artifacts = append(s.artifacts, artifact)
	return nil
}
func (s *spyStore) SaveKnowledge(_ context.Context, knowledge Knowledge) error {
	s.knowledge = append(s.knowledge, knowledge)
	return nil
}
func (s *spyStore) UpdateKnowledge(_ context.Context, knowledge Knowledge) error {
	s.knowledge = append(s.knowledge, knowledge)
	return nil
}

func (s *spyStore) payload() string {
	return fmt.Sprintf("%v %v %v %v %v %v %v %v %v", s.runs, s.events, s.logs, s.verdicts, s.branches, s.attempts, s.observations, s.artifacts, s.knowledge)
}

func TestRedactingStoreRedactsEveryWrite(t *testing.T) {
	secret := "super-secret-value"
	spy := &spyStore{}
	store := newRedactingStore(spy)
	store.register("run-1", NewRedactor(secret))
	ctx := context.Background()
	if err := store.CreateRun(ctx, RunRecord{ID: "run-1", Error: secret, Spec: json.RawMessage(`{"planner":{"models_json":"` + secret + `"}}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(ctx, Event{RunID: "run-1", Data: json.RawMessage(`{"note":"` + secret + `"}`), Raw: json.RawMessage(`{"note":"` + secret + `"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendLog(ctx, LogRecord{RunID: "run-1", Text: secret}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveVerdict(ctx, VerdictRecord{RunID: "run-1", Message: secret, Evidence: json.RawMessage(`{"note":"` + secret + `"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveBranch(ctx, Branch{RunID: "run-1", Topic: secret}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAttempt(ctx, Attempt{RunID: "run-1", Error: secret}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveObservation(ctx, Observation{RunID: "run-1", Summary: secret}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveArtifact(ctx, Artifact{RunID: "run-1", Name: secret}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveKnowledge(ctx, Knowledge{RunID: "run-1", Statement: secret}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateKnowledge(ctx, Knowledge{RunID: "run-1", Statement: secret}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Finalize(ctx, FinalizationRecord{
		Run:     RunRecord{ID: "run-1", Error: secret},
		Verdict: &VerdictRecord{RunID: "run-1", Message: secret, Evidence: json.RawMessage(`{"note":"` + secret + `"}`)},
		Event:   Event{RunID: "run-1", Data: json.RawMessage(`{"error":"` + secret + `"}`)},
	}); err != nil {
		t.Fatal(err)
	}
	if payload := spy.payload(); strings.Contains(payload, secret) {
		t.Fatalf("store boundary persisted a known secret: %s", payload)
	}
}

func TestRedactingStoreStripsFlagsWithoutRegistration(t *testing.T) {
	spy := &spyStore{}
	store := newRedactingStore(spy)
	if _, err := store.AppendLog(context.Background(), LogRecord{RunID: "unknown", Text: "submitted flag{abc123}"}); err != nil {
		t.Fatal(err)
	}
	if payload := spy.payload(); strings.Contains(payload, "abc123") {
		t.Fatalf("flag-like value bypassed an unregistered run: %s", payload)
	}
}

type stubStarter struct{}

func (stubStarter) Start(context.Context, ProcessSpec) (Process, error) {
	return nil, errors.New("unused")
}

func TestRunnerInstallsRedactingStore(t *testing.T) {
	runner, err := NewRunner(Config{Store: &spyStore{}, Starter: stubStarter{}, SkipRecovery: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := runner.store.(*redactingStore); !ok {
		t.Fatalf("runner store = %T, want the redacting boundary", runner.store)
	}
}
