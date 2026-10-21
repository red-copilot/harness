package harness_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sync"

	"github.com/red-copilot/harness"
)

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func ioReadAllLimit(reader io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = 1
	}
	return io.ReadAll(io.LimitReader(reader, limit))
}

type memoryStore struct {
	mu                   sync.Mutex
	runs                 map[string]harness.RunRecord
	events               []harness.Event
	logs                 []harness.LogRecord
	verdicts             map[string]harness.VerdictRecord
	branches             []harness.Branch
	attempts             []harness.Attempt
	observations         []harness.Observation
	artifacts            []harness.Artifact
	knowledge            []harness.Knowledge
	markInterruptedCalls int
	statusLog            []harness.Status
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
func (s *memoryStore) SaveVerdict(_ context.Context, record harness.VerdictRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verdicts[record.RunID] = record
	return nil
}
func (s *memoryStore) GetVerdict(_ context.Context, runID string) (harness.VerdictRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verdicts[runID], nil
}
func (s *memoryStore) SaveBranch(_ context.Context, branch harness.Branch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.branches = append(s.branches, branch)
	return nil
}
func (s *memoryStore) ListBranches(_ context.Context, runID string) ([]harness.Branch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []harness.Branch
	for _, branch := range s.branches {
		if branch.RunID == runID {
			out = append(out, branch)
		}
	}
	return out, nil
}
func (s *memoryStore) SaveAttempt(_ context.Context, attempt harness.Attempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts = append(s.attempts, attempt)
	return nil
}
func (s *memoryStore) ListAttempts(_ context.Context, runID string) ([]harness.Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []harness.Attempt
	for _, attempt := range s.attempts {
		if attempt.RunID == runID {
			out = append(out, attempt)
		}
	}
	return out, nil
}
func (s *memoryStore) SaveObservation(_ context.Context, observation harness.Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observations = append(s.observations, observation)
	return nil
}
func (s *memoryStore) ListObservations(_ context.Context, runID string) ([]harness.Observation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []harness.Observation
	for _, observation := range s.observations {
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
func (s *memoryStore) SaveKnowledge(_ context.Context, knowledge harness.Knowledge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.knowledge = append(s.knowledge, knowledge)
	return nil
}
func (s *memoryStore) UpdateKnowledge(_ context.Context, knowledge harness.Knowledge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.knowledge = append(s.knowledge, knowledge)
	return nil
}
func (s *memoryStore) QueryKnowledge(_ context.Context, query harness.KnowledgeQuery) ([]harness.Knowledge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]harness.Knowledge(nil), s.knowledge...)
	return harness.FilterKnowledge(out, query), nil
}
func (s *memoryStore) MarkInterrupted(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markInterruptedCalls++
	return nil
}
func (s *memoryStore) Close() error { return nil }
