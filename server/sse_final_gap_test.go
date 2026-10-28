package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/store/sqlite"
)

type finalGapStore struct {
	*sqlite.Store
	once         sync.Once
	firstStarted chan struct{}
	releaseFirst chan struct{}
}

func (s *finalGapStore) ListEvents(ctx context.Context, runID string, after int64) ([]harness.Event, error) {
	items, err := s.Store.ListEvents(ctx, runID, after)
	s.once.Do(func() {
		close(s.firstStarted)
		<-s.releaseFirst
	})
	return items, err
}

type blockingRuntime struct{}

func (blockingRuntime) NewSession(context.Context, harness.RuntimeRequest) (harness.AgentSession, error) {
	return &blockingSession{events: make(chan harness.AgentEvent)}, nil
}

type blockingSession struct{ events chan harness.AgentEvent }

func (s *blockingSession) Prompt(ctx context.Context, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}
func (*blockingSession) Steer(context.Context, string) error { return nil }
func (*blockingSession) Reset(context.Context) error         { return nil }
func (s *blockingSession) Events() <-chan harness.AgentEvent { return s.events }
func (*blockingSession) Logs() <-chan harness.LogEvent       { return nil }
func (*blockingSession) Usage() harness.Usage                { return harness.Usage{} }
func (*blockingSession) Abort(context.Context) error         { return nil }
func (*blockingSession) Close(context.Context) error         { return nil }

type successVerifier struct{}

func (successVerifier) Verify(context.Context, harness.VerifyRequest) (harness.Verdict, error) {
	return harness.Verdict{Success: true}, nil
}

func TestSSEDoesNotMissFinalEventWhenRunEndsDuringInitialReplay(t *testing.T) {
	sqliteStore, err := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqliteStore.Close()
	store := &finalGapStore{Store: sqliteStore, firstStarted: make(chan struct{}), releaseFirst: make(chan struct{})}
	runner, err := harness.NewRunner(harness.Config{Store: store, Starter: noopStarter{}, WorkspaceRoot: t.TempDir(), SkipRecovery: true, PollInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	spec := harness.RunSpec{
		Task:      harness.Task{ID: "task", Objective: "test"},
		Target:    harness.TargetSpec{ID: "target", Isolation: harness.IsolationCallerManaged},
		Reasoner:  harness.AgentSpec{ID: "planner", Runtime: blockingRuntime{}},
		Executor:  harness.AgentSpec{ID: "exec", Runtime: blockingRuntime{}},
		Policy:    harness.DefaultRunPolicy(),
		Verifier:  successVerifier{},
		Artifacts: sqliteStore,
	}
	run, err := runner.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	server := New(runner, store, nil)
	server.live[run.ID] = run
	handler := server.Handler()
	rec := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/runs/"+run.ID+"/events", nil)
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, request)
		close(done)
	}()
	select {
	case <-store.firstStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("SSE initial replay did not start")
	}
	if err := run.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-run.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("run did not finalize")
	}
	close(store.releaseFirst)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SSE handler did not finish")
	}
	if !strings.Contains(rec.Body.String(), `"type":"run.finalized"`) {
		t.Fatalf("SSE missed run.finalized: %s", rec.Body.String())
	}
}

type noopStarter struct{}

func (noopStarter) Start(context.Context, harness.ProcessSpec) (harness.Process, error) {
	return nil, context.Canceled
}
