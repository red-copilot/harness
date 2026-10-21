package harness_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

func TestRunnerRequiresVerifierForSuccess(t *testing.T) {
	store := newMemoryStore()
	factory := &fakeFactory{newSession: func(req harness.SessionRequest) harness.AgentSession {
		return &fakeSession{
			result: harness.AgentResult{Status: harness.StatusSucceeded},
			events: [][]byte{[]byte(`{"text":"agent says done"}`)},
		}
	}}
	runner, err := harness.NewRunner(harness.Config{
		Store: store, DefaultFactory: factory, WorkspaceRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	result, err := runner.Run(context.Background(), harness.RunSpec{
		Task:     harness.Task{ID: "demo", Objective: "find the flag"},
		Agents:   []harness.AgentSpec{{ID: "pi"}},
		Verifier: fakeVerifier{verdict: harness.Verdict{Success: false, Message: "not verified"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != harness.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Verdict == nil || result.Verdict.Success {
		t.Fatalf("unexpected verdict: %#v", result.Verdict)
	}
}

func TestRunnerRedactsSecretsBeforeStore(t *testing.T) {
	t.Setenv("TEST_SECRET", "super-secret-value")
	store := newMemoryStore()
	factory := &fakeFactory{newSession: func(req harness.SessionRequest) harness.AgentSession {
		if req.Env["TEST_SECRET"] != "super-secret-value" {
			t.Fatalf("factory did not receive secret env: %#v", req.Env)
		}
		return &fakeSession{
			result: harness.AgentResult{Status: harness.StatusSucceeded},
			events: [][]byte{[]byte(`{"text":"super-secret-value"}`)},
		}
	}}
	runner, err := harness.NewRunner(harness.Config{
		Store: store, DefaultFactory: factory, WorkspaceRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	result, err := runner.Run(context.Background(), harness.RunSpec{
		Task:      harness.Task{ID: "demo", Objective: "secret"},
		Agents:    []harness.AgentSpec{{ID: "pi", EnvFrom: []string{"TEST_SECRET"}}},
		SecretEnv: []string{"TEST_SECRET"},
		Verifier:  fakeVerifier{verdict: harness.Verdict{Success: true}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != harness.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", result.Status)
	}
	events, err := store.ListEvents(context.Background(), result.RunID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	for _, event := range events {
		if string(event.Data) == `{"text":"super-secret-value"}` {
			t.Fatalf("secret leaked into event: %s", event.Data)
		}
	}
}

func TestRunnerHonorsWallTimeAndClosesSession(t *testing.T) {
	store := newMemoryStore()
	closed := make(chan struct{}, 1)
	factory := &fakeFactory{newSession: func(req harness.SessionRequest) harness.AgentSession {
		return &fakeSession{
			block: true,
			onClose: func() {
				select {
				case closed <- struct{}{}:
				default:
				}
			},
		}
	}}
	runner, err := harness.NewRunner(harness.Config{
		Store: store, DefaultFactory: factory, WorkspaceRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	result, err := runner.Run(context.Background(), harness.RunSpec{
		Task:     harness.Task{ID: "timeout", Objective: "wait"},
		Agents:   []harness.AgentSpec{{ID: "pi"}},
		Budget:   harness.Budget{WallTime: 40 * time.Millisecond},
		Verifier: fakeVerifier{verdict: harness.Verdict{Success: true}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != harness.StatusTimedOut {
		t.Fatalf("status = %q, want timed_out", result.Status)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("session was not closed after timeout")
	}
}

func TestRunnerUsesBackendCompletionAndCancelsSessions(t *testing.T) {
	store := newMemoryStore()
	var mu sync.Mutex
	closeCalls := 0
	factory := &fakeFactory{newSession: func(req harness.SessionRequest) harness.AgentSession {
		return &fakeSession{block: true, onClose: func() {
			mu.Lock()
			closeCalls++
			mu.Unlock()
		}}
	}}
	runner, err := harness.NewRunner(harness.Config{
		Store: store, DefaultFactory: factory, WorkspaceRoot: t.TempDir(), PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	backend := &fakeBackend{completion: harness.Completion{Done: true, Success: true, Score: 7}}
	result, err := runner.Run(context.Background(), harness.RunSpec{
		Task:    harness.Task{ID: "backend", Objective: "solve"},
		Backend: backend,
		Agents:  []harness.AgentSpec{{ID: "a"}, {ID: "b"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != harness.StatusSucceeded || result.Verdict == nil || result.Verdict.Score != 7 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(result.Sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(result.Sessions))
	}
	mu.Lock()
	defer mu.Unlock()
	if closeCalls != 2 {
		t.Fatalf("Close calls = %d, want 2", closeCalls)
	}
}

func TestRunnerPropagatesVerifierError(t *testing.T) {
	store := newMemoryStore()
	runner, err := harness.NewRunner(harness.Config{
		Store: store, DefaultFactory: &fakeFactory{newSession: func(req harness.SessionRequest) harness.AgentSession {
			return &fakeSession{result: harness.AgentResult{Status: harness.StatusSucceeded}}
		}}, WorkspaceRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	_, err = runner.Run(context.Background(), harness.RunSpec{
		Task: harness.Task{ID: "verify-error"}, Agents: []harness.AgentSpec{{ID: "a"}},
		Verifier: fakeVerifier{err: errors.New("verifier broke")},
	})
	if err == nil {
		t.Fatal("expected verifier error")
	}
}

type fakeFactory struct {
	mu         sync.Mutex
	newSession func(harness.SessionRequest) harness.AgentSession
}

func (f *fakeFactory) NewSession(_ context.Context, req harness.SessionRequest) (harness.AgentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.newSession(req), nil
}

type fakeSession struct {
	events  [][]byte
	result  harness.AgentResult
	usage   harness.Usage
	block   bool
	onClose func()
	once    sync.Once
}

func (s *fakeSession) Prompt(context.Context, string) error { return nil }
func (s *fakeSession) Steer(context.Context, string) error  { return nil }
func (s *fakeSession) Events() <-chan harness.AgentEvent {
	ch := make(chan harness.AgentEvent, len(s.events))
	for _, event := range s.events {
		ch <- harness.AgentEvent{Type: "test.event", Data: json.RawMessage(event)}
	}
	close(ch)
	return ch
}
func (s *fakeSession) Wait(ctx context.Context) (harness.AgentResult, error) {
	if !s.block {
		return s.result, nil
	}
	<-ctx.Done()
	return harness.AgentResult{Status: harness.StatusCancelled, Error: ctx.Err().Error()}, ctx.Err()
}
func (s *fakeSession) Usage() harness.Usage {
	if s.usage != (harness.Usage{}) {
		return s.usage
	}
	return s.result.Usage
}
func (s *fakeSession) Abort(context.Context) error { return nil }
func (s *fakeSession) Close(context.Context) error {
	s.once.Do(func() {
		if s.onClose != nil {
			s.onClose()
		}
	})
	return nil
}

type fakeVerifier struct {
	verdict harness.Verdict
	err     error
}

func (v fakeVerifier) Verify(context.Context, harness.VerifyRequest) (harness.Verdict, error) {
	return v.verdict, v.err
}

type fakeBackend struct {
	completion harness.Completion
	closed     bool
}

func (b *fakeBackend) Provision(context.Context, harness.ProvisionRequest) (harness.Target, error) {
	return harness.Target{ID: "target", Addresses: []string{"127.0.0.1:1"}}, nil
}
func (b *fakeBackend) Observe(context.Context, harness.Target) (harness.Completion, error) {
	return b.completion, nil
}
func (b *fakeBackend) Close(context.Context, harness.Target) error {
	b.closed = true
	return nil
}

// memoryStore is deliberately small; SQLite has its own tests.
type memoryStore struct {
	mu                   sync.Mutex
	runs                 map[string]harness.RunRecord
	events               []harness.Event
	logs                 []harness.LogRecord
	verdict              map[string]harness.VerdictRecord
	sweeps               map[string]harness.SweepRecord
	nextSeq              int64
	markInterruptedCalls int
}

func newMemoryStore() *memoryStore {
	return &memoryStore{runs: map[string]harness.RunRecord{}, verdict: map[string]harness.VerdictRecord{}, sweeps: map[string]harness.SweepRecord{}}
}
func (s *memoryStore) CreateRun(_ context.Context, r harness.RunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[r.ID] = r
	return nil
}
func (s *memoryStore) UpdateRun(_ context.Context, r harness.RunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[r.ID] = r
	return nil
}
func (s *memoryStore) GetRun(_ context.Context, id string) (harness.RunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[id]
	if !ok {
		return harness.RunRecord{}, errors.New("not found")
	}
	return r, nil
}
func (s *memoryStore) ListRuns(context.Context, int) ([]harness.RunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]harness.RunRecord, 0, len(s.runs))
	for _, r := range s.runs {
		out = append(out, r)
	}
	return out, nil
}
func (s *memoryStore) AppendEvent(_ context.Context, e harness.Event) (harness.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSeq++
	e.Seq = s.nextSeq
	s.events = append(s.events, e)
	return e, nil
}
func (s *memoryStore) ListEvents(_ context.Context, runID string, after int64) ([]harness.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []harness.Event
	for _, e := range s.events {
		if e.RunID == runID && e.Seq > after {
			out = append(out, e)
		}
	}
	return out, nil
}
func (s *memoryStore) AppendLog(_ context.Context, l harness.LogRecord) (harness.LogRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSeq++
	l.Seq = s.nextSeq
	s.logs = append(s.logs, l)
	return l, nil
}
func (s *memoryStore) ListLogs(context.Context, string, int64) ([]harness.LogRecord, error) {
	return nil, nil
}
func (s *memoryStore) SaveVerdict(_ context.Context, v harness.VerdictRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verdict[v.RunID] = v
	return nil
}
func (s *memoryStore) GetVerdict(_ context.Context, runID string) (harness.VerdictRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verdict[runID], nil
}
func (s *memoryStore) SaveSweep(_ context.Context, record harness.SweepRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweeps[record.ID] = record
	return nil
}
func (s *memoryStore) GetSweep(_ context.Context, id string) (harness.SweepRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sweeps[id], nil
}
func (s *memoryStore) ListSweeps(context.Context, int) ([]harness.SweepRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]harness.SweepRecord, 0, len(s.sweeps))
	for _, record := range s.sweeps {
		out = append(out, record)
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

func TestRunnerWaitsForBackendAfterSessionsFinish(t *testing.T) {
	store := newMemoryStore()
	runner, err := harness.NewRunner(harness.Config{
		Store: store, WorkspaceRoot: t.TempDir(), PollInterval: 5 * time.Millisecond,
		DefaultFactory: &fakeFactory{newSession: func(req harness.SessionRequest) harness.AgentSession {
			return &fakeSession{result: harness.AgentResult{Status: harness.StatusSucceeded}}
		}},
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	backend := &delayedBackend{calls: 0}
	result, err := runner.Run(context.Background(), harness.RunSpec{
		Task:    harness.Task{ID: "delayed", Objective: "wait"},
		Backend: backend,
		Agents:  []harness.AgentSpec{{ID: "a"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != harness.StatusSucceeded || backend.calls < 2 {
		t.Fatalf("result = %#v, calls = %d", result, backend.calls)
	}
}

type delayedBackend struct{ calls int }

func (b *delayedBackend) Provision(context.Context, harness.ProvisionRequest) (harness.Target, error) {
	return harness.Target{ID: "t"}, nil
}
func (b *delayedBackend) Observe(context.Context, harness.Target) (harness.Completion, error) {
	b.calls++
	if b.calls >= 2 {
		return harness.Completion{Done: true, Success: true}, nil
	}
	return harness.Completion{}, nil
}
func (b *delayedBackend) Close(context.Context, harness.Target) error { return nil }

func TestRunnerFailsWhenUsageBudgetExceeded(t *testing.T) {
	store := newMemoryStore()
	runner, err := harness.NewRunner(harness.Config{
		Store: store, WorkspaceRoot: t.TempDir(),
		DefaultFactory: &fakeFactory{newSession: func(req harness.SessionRequest) harness.AgentSession {
			return &fakeSession{result: harness.AgentResult{
				Status: harness.StatusSucceeded,
				Usage:  harness.Usage{ToolCalls: 2},
			}}
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), harness.RunSpec{
		Task:     harness.Task{ID: "budget", Objective: "solve"},
		Agents:   []harness.AgentSpec{{ID: "a"}},
		Budget:   harness.Budget{MaxToolCalls: 1},
		Verifier: fakeVerifier{verdict: harness.Verdict{Success: true}},
	})
	if err == nil || !strings.Contains(err.Error(), "budget exceeded") {
		t.Fatalf("expected budget error, result=%#v err=%v", result, err)
	}
}

func TestRunnerStopsLiveBudgetExceeded(t *testing.T) {
	store := newMemoryStore()
	runner, err := harness.NewRunner(harness.Config{
		Store: store, WorkspaceRoot: t.TempDir(),
		DefaultFactory: &fakeFactory{newSession: func(req harness.SessionRequest) harness.AgentSession {
			return &fakeSession{block: true, usage: harness.Usage{ToolCalls: 2}}
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.Run(context.Background(), harness.RunSpec{
		Task:   harness.Task{ID: "live-budget", Objective: "solve"},
		Agents: []harness.AgentSpec{{ID: "a"}},
		Budget: harness.Budget{MaxToolCalls: 1},
	})
	if err == nil || !strings.Contains(err.Error(), "budget exceeded") {
		t.Fatalf("expected live budget error, got %v", err)
	}
}

func TestRunnerCanSkipRecoveryForObservabilityProcess(t *testing.T) {
	store := newMemoryStore()
	cleaner := &recordingCleaner{}
	runner, err := harness.NewRunner(harness.Config{
		Store: store, Cleaner: cleaner, SkipRecovery: true, WorkspaceRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	defer runner.Close(context.Background())
	if cleaner.calls != 0 {
		t.Fatalf("cleanup calls = %d, want 0", cleaner.calls)
	}
	if store.markInterruptedCalls != 0 {
		t.Fatalf("MarkInterrupted calls = %d, want 0", store.markInterruptedCalls)
	}
}

type recordingCleaner struct{ calls int }

func (c *recordingCleaner) CleanupOrphans(context.Context) error {
	c.calls++
	return nil
}
