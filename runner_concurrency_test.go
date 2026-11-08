package harness_test

import (
	"context"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

func TestRunnerMaxSessionsAppliesAcrossRuns(t *testing.T) {
	store := newMemoryStore()
	opened := make(chan struct{}, 2)
	blockingRuntime := &fakeRuntime{new: func(harness.RuntimeRequest) harness.AgentSession {
		opened <- struct{}{}
		return &fakeSession{
			events: make(chan harness.AgentEvent),
			prompt: func(ctx context.Context, _ string) error {
				<-ctx.Done()
				return ctx.Err()
			},
		}
	}}
	spec := integrationSpec(t, store)
	spec.Reasoner.Runtime = blockingRuntime
	spec.Executor.Runtime = blockingRuntime
	runner, err := harness.NewRunner(harness.Config{
		Store: store, Starter: noopStarter{}, WorkspaceRoot: t.TempDir(), MaxSessions: 2, SkipRecovery: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())

	first, err := runner.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-opened:
		case <-time.After(time.Second):
			t.Fatalf("first run opened %d sessions, want 2", i)
		}
	}

	secondResult := make(chan *harness.Run, 1)
	secondErr := make(chan error, 1)
	go func() {
		run, err := runner.Start(context.Background(), spec)
		if err != nil {
			secondErr <- err
			return
		}
		secondResult <- run
	}()
	select {
	case run := <-secondResult:
		t.Fatalf("second run started while %d/%d session slots were occupied: %s", 2, 2, run.ID)
	case err := <-secondErr:
		t.Fatalf("second run failed instead of waiting: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := first.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("first run did not release session slots")
	}

	var second *harness.Run
	select {
	case second = <-secondResult:
	case err := <-secondErr:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("second run did not start after slots were released")
	}
	_ = second.Cancel(context.Background())
	select {
	case <-second.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("second run did not finish")
	}
}
