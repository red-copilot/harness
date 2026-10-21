package harness

import (
	"context"
	"sync"
)

// redactingStore is the canonical persistence boundary. Every write is
// redacted here, so a call site that forgets to redact cannot persist a known
// secret or an obvious flag. The composition root installs it around the
// caller-supplied store.
type redactingStore struct {
	inner Store

	mu    sync.RWMutex
	byRun map[string]*Redactor
}

func newRedactingStore(inner Store) *redactingStore {
	return &redactingStore{inner: inner, byRun: map[string]*Redactor{}}
}

// register binds the Run's secret values before its first write.
func (s *redactingStore) register(runID string, redactor *Redactor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byRun[runID] = redactor
}

func (s *redactingStore) unregister(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byRun, runID)
}

// redactor returns the Run's redactor, or a flag-only redactor for a Run that
// was never registered. Flag-like values are always redacted.
func (s *redactingStore) redactor(runID string) *Redactor {
	s.mu.RLock()
	redactor := s.byRun[runID]
	s.mu.RUnlock()
	if redactor != nil {
		return redactor
	}
	return NewRedactor()
}

func (s *redactingStore) CreateRun(ctx context.Context, record RunRecord) error {
	return s.inner.CreateRun(ctx, s.redactRun(record))
}

func (s *redactingStore) UpdateRun(ctx context.Context, record RunRecord) error {
	return s.inner.UpdateRun(ctx, s.redactRun(record))
}

func (s *redactingStore) redactRun(record RunRecord) RunRecord {
	redactor := s.redactor(record.ID)
	record.Error = redactor.Redact(record.Error)
	record.Spec = redactJSONPreserve(redactor, record.Spec)
	record.Result = redactJSONPreserve(redactor, record.Result)
	record.Metadata = redactJSONPreserve(redactor, record.Metadata)
	return record
}

func (s *redactingStore) GetRun(ctx context.Context, id string) (RunRecord, error) {
	return s.inner.GetRun(ctx, id)
}

func (s *redactingStore) ListRuns(ctx context.Context, limit int) ([]RunRecord, error) {
	return s.inner.ListRuns(ctx, limit)
}

func (s *redactingStore) MarkInterrupted(ctx context.Context) error {
	return s.inner.MarkInterrupted(ctx)
}

func (s *redactingStore) AppendEvent(ctx context.Context, event Event) (Event, error) {
	redactor := s.redactor(event.RunID)
	if redactor != nil {
		shared := len(event.Raw) > 0 && string(event.Raw) == string(event.Data)
		event.Data = redactJSONPreserve(redactor, event.Data)
		if shared {
			event.Raw = event.Data
		} else if len(event.Raw) > 0 {
			event.Raw = redactJSONPreserve(redactor, event.Raw)
		}
	}
	return s.inner.AppendEvent(ctx, event)
}

func (s *redactingStore) ListEvents(ctx context.Context, runID string, after int64) ([]Event, error) {
	return s.inner.ListEvents(ctx, runID, after)
}

func (s *redactingStore) AppendLog(ctx context.Context, record LogRecord) (LogRecord, error) {
	record.Text = s.redactor(record.RunID).Redact(record.Text)
	return s.inner.AppendLog(ctx, record)
}

func (s *redactingStore) ListLogs(ctx context.Context, runID string, after int64) ([]LogRecord, error) {
	return s.inner.ListLogs(ctx, runID, after)
}

func (s *redactingStore) SaveVerdict(ctx context.Context, record VerdictRecord) error {
	redactor := s.redactor(record.RunID)
	record.Message = redactor.Redact(record.Message)
	record.Evidence = redactJSONPreserve(redactor, record.Evidence)
	return s.inner.SaveVerdict(ctx, record)
}

func (s *redactingStore) GetVerdict(ctx context.Context, runID string) (VerdictRecord, error) {
	return s.inner.GetVerdict(ctx, runID)
}

func (s *redactingStore) SaveBranch(ctx context.Context, branch Branch) error {
	return s.inner.SaveBranch(ctx, redactBranch(s.redactor(branch.RunID), branch))
}

func (s *redactingStore) ListBranches(ctx context.Context, runID string) ([]Branch, error) {
	return s.inner.ListBranches(ctx, runID)
}

func (s *redactingStore) SaveAttempt(ctx context.Context, attempt Attempt) error {
	return s.inner.SaveAttempt(ctx, redactAttempt(s.redactor(attempt.RunID), attempt))
}

func (s *redactingStore) ListAttempts(ctx context.Context, runID string) ([]Attempt, error) {
	return s.inner.ListAttempts(ctx, runID)
}

func (s *redactingStore) SaveObservation(ctx context.Context, observation Observation) error {
	return s.inner.SaveObservation(ctx, redactObservation(s.redactor(observation.RunID), observation))
}

func (s *redactingStore) ListObservations(ctx context.Context, runID string) ([]Observation, error) {
	return s.inner.ListObservations(ctx, runID)
}

func (s *redactingStore) SaveArtifact(ctx context.Context, artifact Artifact) error {
	return s.inner.SaveArtifact(ctx, redactArtifact(s.redactor(artifact.RunID), artifact))
}

func (s *redactingStore) ListArtifacts(ctx context.Context, runID string) ([]Artifact, error) {
	return s.inner.ListArtifacts(ctx, runID)
}

func (s *redactingStore) SaveKnowledge(ctx context.Context, knowledge Knowledge) error {
	return s.inner.SaveKnowledge(ctx, redactKnowledge(s.redactor(knowledge.RunID), knowledge))
}

func (s *redactingStore) UpdateKnowledge(ctx context.Context, knowledge Knowledge) error {
	return s.inner.UpdateKnowledge(ctx, redactKnowledge(s.redactor(knowledge.RunID), knowledge))
}

func (s *redactingStore) QueryKnowledge(ctx context.Context, query KnowledgeQuery) ([]Knowledge, error) {
	return s.inner.QueryKnowledge(ctx, query)
}

func (s *redactingStore) Close() error { return s.inner.Close() }

var _ Store = (*redactingStore)(nil)
