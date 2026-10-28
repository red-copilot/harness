package harness_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

type memoryStore struct {
	mu                   sync.Mutex
	runs                 map[string]harness.RunRecord
	events               []harness.Event
	logs                 []harness.LogRecord
	verdicts             map[string]harness.VerdictRecord
	facts                []harness.Fact
	intents              []harness.Intent
	hints                []harness.Hint
	intentAttempts       []harness.IntentAttempt
	boardObservations    []harness.Observation
	artifacts            []harness.Artifact
	artifactData         map[string][]byte
	markInterruptedCalls int
	statusLog            []harness.Status
	boardRevision        uint64
}

func newMemoryStore() *memoryStore {
	return &memoryStore{runs: map[string]harness.RunRecord{}, verdicts: map[string]harness.VerdictRecord{}}
}

func (s *memoryStore) CreateRun(_ context.Context, record harness.RunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[record.ID] = record
	s.statusLog = append(s.statusLog, record.Status)
	return nil
}
func (s *memoryStore) UpdateRun(_ context.Context, record harness.RunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[record.ID] = record
	s.statusLog = append(s.statusLog, record.Status)
	return nil
}
func (s *memoryStore) Finalize(_ context.Context, record harness.FinalizationRecord) (harness.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[record.Run.ID] = record.Run
	s.statusLog = append(s.statusLog, record.Run.Status)
	if record.Verdict != nil {
		s.verdicts[record.Verdict.RunID] = *record.Verdict
	}
	event := record.Event
	event.Seq = int64(len(s.events) + 1)
	if event.SchemaVersion == 0 {
		event.SchemaVersion = 1
	}
	s.events = append(s.events, event)
	return event, nil
}
func (s *memoryStore) GetRun(_ context.Context, id string) (harness.RunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs[id], nil
}
func (s *memoryStore) ListRuns(context.Context, int) ([]harness.RunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]harness.RunRecord, 0, len(s.runs))
	for _, record := range s.runs {
		out = append(out, record)
	}
	return out, nil
}
func (s *memoryStore) AppendEvent(_ context.Context, event harness.Event) (harness.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	event.Seq = int64(len(s.events) + 1)
	if event.SchemaVersion == 0 {
		event.SchemaVersion = 1
	}
	s.events = append(s.events, event)
	return event, nil
}
func (s *memoryStore) ListEvents(_ context.Context, runID string, after int64) ([]harness.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []harness.Event
	for _, event := range s.events {
		if event.RunID == runID && event.Seq > after {
			out = append(out, event)
		}
	}
	return out, nil
}
func (s *memoryStore) AppendLog(_ context.Context, record harness.LogRecord) (harness.LogRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record.Seq = int64(len(s.logs) + 1)
	s.logs = append(s.logs, record)
	return record, nil
}
func (s *memoryStore) ListLogs(_ context.Context, runID string, after int64) ([]harness.LogRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []harness.LogRecord
	for _, record := range s.logs {
		if record.RunID == runID && record.Seq > after {
			out = append(out, record)
		}
	}
	return out, nil
}
func (s *memoryStore) GetVerdict(_ context.Context, runID string) (harness.VerdictRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verdicts[runID], nil
}

func (s *memoryStore) Commit(_ context.Context, commit harness.Commit) ([]harness.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if commit.Revision > s.boardRevision {
		s.boardRevision = commit.Revision
	}
	s.facts = append(s.facts, commit.Facts...)
	for _, intent := range commit.Intents {
		replaced := false
		for i := range s.intents {
			if s.intents[i].RunID == intent.RunID && s.intents[i].ID == intent.ID {
				s.intents[i] = intent
				replaced = true
			}
		}
		if !replaced {
			s.intents = append(s.intents, intent)
		}
	}
	s.hints = append(s.hints, commit.Hints...)
	for _, attempt := range commit.Attempts {
		replaced := false
		for i := range s.intentAttempts {
			if s.intentAttempts[i].RunID == attempt.RunID && s.intentAttempts[i].ID == attempt.ID {
				s.intentAttempts[i] = attempt
				replaced = true
			}
		}
		if !replaced {
			s.intentAttempts = append(s.intentAttempts, attempt)
		}
	}
	s.boardObservations = append(s.boardObservations, commit.Observations...)
	s.artifacts = append(s.artifacts, commit.Artifacts...)
	for _, event := range commit.Events {
		event.Seq = int64(len(s.events) + 1)
		s.events = append(s.events, event)
	}
	return append([]harness.Event(nil), commit.Events...), nil
}

func (s *memoryStore) SaveFact(_ context.Context, fact harness.Fact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.facts = append(s.facts, fact)
	return nil
}
func (s *memoryStore) ListFacts(_ context.Context, runID string) ([]harness.Fact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []harness.Fact
	for _, fact := range s.facts {
		if fact.RunID == runID {
			out = append(out, fact)
		}
	}
	return out, nil
}
func (s *memoryStore) SaveIntent(_ context.Context, intent harness.Intent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.intents {
		if s.intents[i].RunID == intent.RunID && s.intents[i].ID == intent.ID {
			s.intents[i] = intent
			return nil
		}
	}
	s.intents = append(s.intents, intent)
	return nil
}
func (s *memoryStore) ListIntents(_ context.Context, runID string) ([]harness.Intent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []harness.Intent
	for _, intent := range s.intents {
		if intent.RunID == runID {
			out = append(out, intent)
		}
	}
	return out, nil
}
func (s *memoryStore) SaveHint(_ context.Context, hint harness.Hint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hints = append(s.hints, hint)
	return nil
}
func (s *memoryStore) ListHints(_ context.Context, runID string) ([]harness.Hint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []harness.Hint
	for _, hint := range s.hints {
		if hint.RunID == runID {
			out = append(out, hint)
		}
	}
	return out, nil
}
func (s *memoryStore) SaveIntentAttempt(_ context.Context, attempt harness.IntentAttempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.intentAttempts {
		if s.intentAttempts[i].RunID == attempt.RunID && s.intentAttempts[i].ID == attempt.ID {
			s.intentAttempts[i] = attempt
			return nil
		}
	}
	s.intentAttempts = append(s.intentAttempts, attempt)
	return nil
}
func (s *memoryStore) ListIntentAttempts(_ context.Context, runID string) ([]harness.IntentAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []harness.IntentAttempt
	for _, attempt := range s.intentAttempts {
		if attempt.RunID == runID {
			out = append(out, attempt)
		}
	}
	return out, nil
}
func (s *memoryStore) SaveBlackboardObservation(_ context.Context, observation harness.Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.boardObservations = append(s.boardObservations, observation)
	return nil
}
func (s *memoryStore) ListBlackboardObservations(_ context.Context, runID string) ([]harness.Observation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []harness.Observation
	for _, observation := range s.boardObservations {
		if observation.RunID == runID {
			out = append(out, observation)
		}
	}
	return out, nil
}
func (s *memoryStore) SaveArtifact(_ context.Context, artifact harness.Artifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.artifacts = append(s.artifacts, artifact)
	return nil
}
func (s *memoryStore) ListArtifacts(_ context.Context, runID string) ([]harness.Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []harness.Artifact
	for _, artifact := range s.artifacts {
		if artifact.RunID == runID {
			out = append(out, artifact)
		}
	}
	return out, nil
}
func (s *memoryStore) MarkInterrupted(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markInterruptedCalls++
	return nil
}
func (s *memoryStore) Close() error { return nil }

type fakeSession struct {
	events   chan harness.AgentEvent
	usage    harness.Usage
	mu       sync.Mutex
	aborts   int
	closes   int
	closeErr error
	prompt   func(context.Context, string) error
}

func (s *fakeSession) Prompt(ctx context.Context, prompt string) error {
	if s.prompt != nil {
		return s.prompt(ctx, prompt)
	}
	select {
	case s.events <- harness.AgentEvent{Type: "agent_settled", Kind: harness.AgentEventSettled, Data: json.RawMessage(`{"type":"agent_settled"}`)}:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}
func (s *fakeSession) Steer(context.Context, string) error { return nil }
func (s *fakeSession) Reset(context.Context) error         { return nil }
func (s *fakeSession) Events() <-chan harness.AgentEvent   { return s.events }
func (s *fakeSession) Logs() <-chan harness.LogEvent       { return nil }
func (s *fakeSession) Usage() harness.Usage                { return s.usage }
func (s *fakeSession) Abort(context.Context) error {
	s.mu.Lock()
	s.aborts++
	s.mu.Unlock()
	return nil
}
func (s *fakeSession) Close(context.Context) error {
	s.mu.Lock()
	s.closes++
	err := s.closeErr
	s.mu.Unlock()
	return err
}

type fakeRuntime struct {
	mu       sync.Mutex
	requests []harness.RuntimeRequest
	new      func(harness.RuntimeRequest) harness.AgentSession
}

func (r *fakeRuntime) NewSession(_ context.Context, req harness.RuntimeRequest) (harness.AgentSession, error) {
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.mu.Unlock()
	return r.new(req), nil
}

type fakeVerifier struct{ verdict harness.Verdict }

func (v fakeVerifier) Verify(context.Context, harness.VerifyRequest) (harness.Verdict, error) {
	return v.verdict, nil
}

func promptSession(text string) *fakeSession {
	s := &fakeSession{events: make(chan harness.AgentEvent, 8)}
	s.prompt = func(ctx context.Context, _ string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		s.events <- harness.AgentEvent{Kind: harness.AgentEventMessage, Text: text}
		s.events <- harness.AgentEvent{Kind: harness.AgentEventSettled}
		return nil
	}
	return s
}

func integrationSpec(t *testing.T, store harness.Store) harness.RunSpec {
	t.Helper()
	reasonerRuntime := &fakeRuntime{new: func(harness.RuntimeRequest) harness.AgentSession {
		return promptSession(`{"frontier_version":1,"intents":[{"from":["origin"],"description":"probe"}]}`)
	}}
	executorRuntime := &fakeRuntime{new: func(harness.RuntimeRequest) harness.AgentSession {
		return promptSession(`{"phase":"concluded","fact":{"kind":"negative","description":"no signal"}}`)
	}}
	policy := harness.DefaultRunPolicy()
	policy.Exploration.MaxAttemptsPerIntent = 1
	policy.Exploration.StagnationRounds = 1
	policy.Exploration.MaxWallTimePerAttempt = time.Second
	policy.Exploration.ConcludeTimeout = 100 * time.Millisecond
	policy.Exploration.MaxReasonerWallTime = 250 * time.Millisecond
	policy.Exploration.MaxReasonerFailures = 1
	return harness.RunSpec{
		Task:     harness.Task{ID: "task-1", Objective: "test the authorized target"},
		Target:   harness.TargetSpec{ID: "target-1", Addresses: []string{"10.0.0.1:80"}, Isolation: harness.IsolationCallerManaged},
		Reasoner: harness.AgentSpec{ID: "reasoner", Runtime: reasonerRuntime},
		Executors: []harness.AgentSpec{
			{ID: "executor-1", Runtime: executorRuntime},
			{ID: "executor-2", Runtime: executorRuntime},
		},
		Policy:         policy,
		ReasonStrategy: noIntentReasonStrategy{},
		Verifier:       fakeVerifier{verdict: harness.Verdict{Success: false}},
		Artifacts:      store.(harness.ArtifactStore),
	}
}

func attachBackend(spec *harness.RunSpec, backend harness.Backend) {
	spec.Provisioner = backend
	spec.Backend = backend
}

func newTestRunner(t *testing.T, store harness.Store) *harness.Runner {
	t.Helper()
	runner, err := harness.NewRunner(harness.Config{Store: store, Starter: noopStarter{}, WorkspaceRoot: t.TempDir(), SkipRecovery: true})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

type fakeArtifactStore struct {
	puts  int
	fail  error
	items map[string][]byte
}

func (s *fakeArtifactStore) Put(_ context.Context, input harness.ArtifactInput) (harness.Artifact, error) {
	if s.fail != nil {
		return harness.Artifact{}, s.fail
	}
	data, err := io.ReadAll(input.Content)
	if err != nil {
		return harness.Artifact{}, err
	}
	s.puts++
	if s.items == nil {
		s.items = map[string][]byte{}
	}
	id := harness.ArtifactContentID(data)
	s.items[id] = data
	return harness.Artifact{ID: id, RunID: input.RunID, AttemptID: input.AttemptID, SessionID: input.SessionID, Name: input.Name, MediaType: input.MediaType, Size: int64(len(data)), SHA256: id, Redacted: true}, nil
}

func (s *fakeArtifactStore) Open(_ context.Context, id string) (io.ReadCloser, error) {
	data, ok := s.items[id]
	if !ok {
		return nil, errors.New("artifact not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *memoryStore) Put(_ context.Context, input harness.ArtifactInput) (harness.Artifact, error) {
	data, err := io.ReadAll(input.Content)
	if err != nil {
		return harness.Artifact{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.artifactData == nil {
		s.artifactData = map[string][]byte{}
	}
	id := harness.ArtifactContentID(data)
	s.artifactData[id] = append([]byte(nil), data...)
	return harness.Artifact{ID: id, RunID: input.RunID, AttemptID: input.AttemptID, SessionID: input.SessionID, Name: input.Name, MediaType: input.MediaType, Size: int64(len(data)), SHA256: id, CreatedAt: time.Now().UTC()}, nil
}

func (s *memoryStore) Open(_ context.Context, id string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.artifactData[id]
	if !ok {
		return nil, errors.New("artifact not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

type noIntentReasonStrategy struct{}

func (noIntentReasonStrategy) Prompt(harness.ReasonRequest) (string, error) { return "reason", nil }
func (noIntentReasonStrategy) Parse(req harness.ReasonRequest, _ harness.TurnResult) (harness.ReasonReport, error) {
	return harness.ReasonReport{FrontierVersion: req.FrontierVersion}, nil
}

func (s *memoryStore) GetBoardRevision(_ context.Context, _ string) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.boardRevision, nil
}
