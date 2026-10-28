package engine

import (
	"context"
	"sync"

	"github.com/red-copilot/harness/core"
)

// redactingStore is the canonical persistence boundary.
type redactingStore struct {
	inner Store

	mu    sync.RWMutex
	byRun map[string]*core.Redactor
}

func newRedactingStore(inner Store) *redactingStore {
	return &redactingStore{inner: inner, byRun: map[string]*core.Redactor{}}
}

func (s *redactingStore) register(runID string, redactor *core.Redactor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byRun[runID] = redactor
}

func (s *redactingStore) unregister(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byRun, runID)
}

func (s *redactingStore) redactor(runID string) *core.Redactor {
	s.mu.RLock()
	redactor := s.byRun[runID]
	s.mu.RUnlock()
	if redactor != nil {
		return redactor
	}
	return core.NewRedactor()
}

func (s *redactingStore) redactRun(record RunRecord) RunRecord {
	r := s.redactor(record.ID)
	record.Error = r.Redact(record.Error)
	record.Spec = r.RedactJSON(record.Spec)
	record.Result = r.RedactJSON(record.Result)
	record.Metadata = r.RedactJSON(record.Metadata)
	return record
}

func (s *redactingStore) redactEvent(event Event) Event {
	r := s.redactor(event.RunID)
	shared := len(event.Raw) > 0 && string(event.Raw) == string(event.Data)
	event.Data = r.RedactJSON(event.Data)
	if shared {
		event.Raw = event.Data
	} else if len(event.Raw) > 0 {
		event.Raw = r.RedactJSON(event.Raw)
	}
	return event
}

func (s *redactingStore) CreateRun(ctx context.Context, record RunRecord) error {
	return s.inner.CreateRun(ctx, s.redactRun(record))
}

func (s *redactingStore) UpdateRun(ctx context.Context, record RunRecord) error {
	return s.inner.UpdateRun(ctx, s.redactRun(record))
}

func (s *redactingStore) AppendEvent(ctx context.Context, event Event) (Event, error) {
	return s.inner.AppendEvent(ctx, s.redactEvent(event))
}

func (s *redactingStore) AppendLog(ctx context.Context, record LogRecord) (LogRecord, error) {
	record.Text = s.redactor(record.RunID).Redact(record.Text)
	return s.inner.AppendLog(ctx, record)
}

func (s *redactingStore) Commit(ctx context.Context, commit Commit) ([]Event, error) {
	r := s.redactor(commit.RunID)
	for i := range commit.Facts {
		commit.Facts[i] = core.RedactFact(r, commit.Facts[i])
	}
	for i := range commit.Intents {
		commit.Intents[i] = core.RedactIntent(r, commit.Intents[i])
	}
	for i := range commit.Hints {
		commit.Hints[i] = core.RedactHint(r, commit.Hints[i])
	}
	for i := range commit.Attempts {
		commit.Attempts[i] = core.RedactIntentAttempt(r, commit.Attempts[i])
	}
	for i := range commit.Observations {
		commit.Observations[i] = core.RedactObservation(r, commit.Observations[i])
	}
	for i := range commit.Artifacts {
		commit.Artifacts[i] = core.RedactArtifact(r, commit.Artifacts[i])
	}
	for i := range commit.Events {
		commit.Events[i] = s.redactEvent(commit.Events[i])
	}
	return s.inner.Commit(ctx, commit)
}

func (s *redactingStore) Finalize(ctx context.Context, record FinalizationRecord) (Event, error) {
	record.Run = s.redactRun(record.Run)
	if record.Verdict != nil {
		r := s.redactor(record.Verdict.RunID)
		verdict := *record.Verdict
		verdict.Message = r.Redact(verdict.Message)
		verdict.Evidence = r.RedactJSON(verdict.Evidence)
		record.Verdict = &verdict
	}
	record.Event = s.redactEvent(record.Event)
	return s.inner.Finalize(ctx, record)
}

func (s *redactingStore) GetRun(ctx context.Context, id string) (RunRecord, error) {
	return s.inner.GetRun(ctx, id)
}
func (s *redactingStore) ListRuns(ctx context.Context, limit int) ([]RunRecord, error) {
	return s.inner.ListRuns(ctx, limit)
}
func (s *redactingStore) ListEvents(ctx context.Context, runID string, after int64) ([]Event, error) {
	return s.inner.ListEvents(ctx, runID, after)
}
func (s *redactingStore) ListLogs(ctx context.Context, runID string, after int64) ([]LogRecord, error) {
	return s.inner.ListLogs(ctx, runID, after)
}
func (s *redactingStore) GetVerdict(ctx context.Context, runID string) (VerdictRecord, error) {
	return s.inner.GetVerdict(ctx, runID)
}
func (s *redactingStore) ListFacts(ctx context.Context, runID string) ([]Fact, error) {
	return s.inner.ListFacts(ctx, runID)
}
func (s *redactingStore) ListIntents(ctx context.Context, runID string) ([]Intent, error) {
	return s.inner.ListIntents(ctx, runID)
}
func (s *redactingStore) ListHints(ctx context.Context, runID string) ([]Hint, error) {
	return s.inner.ListHints(ctx, runID)
}
func (s *redactingStore) ListIntentAttempts(ctx context.Context, runID string) ([]IntentAttempt, error) {
	return s.inner.ListIntentAttempts(ctx, runID)
}
func (s *redactingStore) ListBlackboardObservations(ctx context.Context, runID string) ([]Observation, error) {
	return s.inner.ListBlackboardObservations(ctx, runID)
}
func (s *redactingStore) ListArtifacts(ctx context.Context, runID string) ([]Artifact, error) {
	return s.inner.ListArtifacts(ctx, runID)
}
func (s *redactingStore) MarkInterrupted(ctx context.Context) error {
	return s.inner.MarkInterrupted(ctx)
}
func (s *redactingStore) Close() error { return s.inner.Close() }

var _ Store = (*redactingStore)(nil)

func (s *redactingStore) GetBoardRevision(ctx context.Context, runID string) (uint64, error) {
	return s.inner.GetBoardRevision(ctx, runID)
}
