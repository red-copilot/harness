package kernel_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/red-copilot/harness/core"
	"github.com/red-copilot/harness/engine"
	"github.com/red-copilot/harness/engine/kernel"
	"github.com/red-copilot/harness/runtime"
	"github.com/red-copilot/harness/store/sqlite"
)

type testClock struct {
	mu   sync.Mutex
	tick int64
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tick++
	return time.Unix(1_700_000_000, c.tick*int64(time.Millisecond)).UTC()
}

type testIDs struct {
	mu   sync.Mutex
	next uint64
}

func (i *testIDs) NewRunID() (string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.next++
	return "run-" + itoa(i.next), nil
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for v > 0 {
		pos--
		buf[pos] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[pos:])
}

type testSecrets struct{}

func (testSecrets) LookupEnv(name string) (string, bool) { return os.LookupEnv(name) }

type fakeSession struct {
	events chan engine.AgentEvent
	mu     sync.Mutex
	calls  int
	prompt func(context.Context, string, int) error
}

func (s *fakeSession) Prompt(ctx context.Context, text string) error {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if s.prompt != nil {
		return s.prompt(ctx, text, call)
	}
	select {
	case s.events <- engine.AgentEvent{Kind: engine.AgentEventSettled}:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}
func (s *fakeSession) Reset(context.Context) error         { return nil }
func (s *fakeSession) Events() <-chan engine.AgentEvent    { return s.events }
func (s *fakeSession) Logs() <-chan core.LogEvent          { return nil }
func (s *fakeSession) Abort(context.Context) error         { return nil }
func (s *fakeSession) Close(context.Context) error         { return nil }
func (s *fakeSession) Steer(context.Context, string) error { return nil }

func (s *fakeSession) Usage() core.Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return core.Usage{
		InputTokens: 10 * s.calls, OutputTokens: 5 * s.calls, TotalTokens: 15 * s.calls,
		ModelCalls: s.calls, ToolCalls: 2 * s.calls, CostUSD: 0.01 * float64(s.calls),
	}
}

type fakeRuntime struct {
	mu  sync.Mutex
	new func(engine.RuntimeRequest) engine.AgentSession
}

func (r *fakeRuntime) NewSession(_ context.Context, req engine.RuntimeRequest) (engine.AgentSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.new(req), nil
}

func writeSession(workspace, report string, evidence map[string]string) *fakeSession {
	session := &fakeSession{events: make(chan engine.AgentEvent, 8)}
	session.prompt = func(ctx context.Context, _ string, _ int) error {
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			return err
		}
		for name, content := range evidence {
			if err := os.WriteFile(filepath.Join(workspace, name), []byte(content), 0o600); err != nil {
				return err
			}
		}
		if err := os.WriteFile(filepath.Join(workspace, "report.json"), []byte(report), 0o600); err != nil {
			return err
		}
		select {
		case session.events <- engine.AgentEvent{Kind: engine.AgentEventSettled}:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}
	return session
}

// Reason strategies.
type scriptedReason struct {
	mu        sync.Mutex
	calls     int
	goalFirst bool
	zero      bool
	delay     time.Duration
}

func (s *scriptedReason) Prompt(core.ReasonRequest) (string, error) {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	return "reason", nil
}

func (s *scriptedReason) Parse(req core.ReasonRequest, _ core.TurnResult) (core.ReasonReport, error) {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if s.zero {
		return core.ReasonReport{BoardRevision: req.BoardRevision}, nil
	}
	if s.goalFirst || call > 1 {
		return core.ReasonReport{BoardRevision: req.BoardRevision, GoalCandidate: &core.GoalCandidate{
			From: []string{"origin"}, Description: "evidence should satisfy the goal",
		}}, nil
	}
	return core.ReasonReport{BoardRevision: req.BoardRevision, Intents: []core.IntentProposal{{
		From: []string{"origin"}, Description: "probe the target",
	}}}, nil
}

type testExplore struct{}

func (testExplore) ExecutePrompt(core.ExploreRequest) (string, error)  { return "explore", nil }
func (testExplore) ConcludePrompt(core.ExploreRequest) (string, error) { return "conclude", nil }
func (testExplore) Parse(req core.ExploreRequest, _ core.TurnResult) (core.AttemptReport, error) {
	return core.AttemptReport{
		IntentID: req.Intent.ID, Phase: core.IntentAttemptConcluded,
		Claim: &core.ClaimProposal{Kind: core.ClaimNegative, Description: "no signal", ObservationRefs: []string{"obs-1"}},
		Observations: []core.ObservationProposal{{
			LocalID: "obs-1", Kind: "test.result", Summary: "no signal", Target: "target-1",
			EvidenceFiles: []string{"evidence.txt"},
		}},
	}, nil
}

type testVerifier struct {
	mu    sync.Mutex
	calls int
	fn    func(context.Context, core.VerifyRequest) (core.Verdict, error)
}

func (v *testVerifier) Verify(ctx context.Context, req core.VerifyRequest) (core.Verdict, error) {
	v.mu.Lock()
	v.calls++
	v.mu.Unlock()
	if v.fn != nil {
		return v.fn(ctx, req)
	}
	return core.Verdict{Success: true, Score: 70}, nil
}

func (v *testVerifier) count() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.calls
}

func testPolicy() core.RunPolicy {
	policy := core.DefaultRunPolicy()
	policy.Exploration.MaxAttemptsPerIntent = 1
	policy.Exploration.MaxTotalIntents = 4
	policy.Exploration.EnginePollInterval = 5 * time.Millisecond
	policy.Exploration.MaxReasonerWallTime = 2 * time.Second
	policy.Exploration.MaxWallTimePerAttempt = 2 * time.Second
	policy.Exploration.ConcludeTimeout = time.Second
	policy.Exploration.DrainTimeout = 500 * time.Millisecond
	policy.Exploration.MaxReasonerFailures = 2
	return policy
}

func testSpec(reason core.ReasonStrategy, verifier engine.Verifier) kernel.Spec {
	reasonRuntime := &fakeRuntime{new: func(req engine.RuntimeRequest) engine.AgentSession {
		return writeSession(req.WorkspaceDir, `{}`, nil)
	}}
	executorRuntime := &fakeRuntime{new: func(req engine.RuntimeRequest) engine.AgentSession {
		return writeSession(req.WorkspaceDir, `{}`, map[string]string{"evidence.txt": "no signal"})
	}}
	return kernel.Spec{
		Task:            core.Task{ID: "task-1", Objective: "test the authorized target"},
		Target:          core.TargetSpec{ID: "target-1", Addresses: []string{"10.0.0.1:80"}, Isolation: core.IsolationCallerManaged},
		Policy:          testPolicy(),
		Reasoner:        engine.AgentSpec{ID: "reasoner", Runtime: reasonRuntime},
		Executor:        engine.AgentSpec{ID: "executor", Runtime: executorRuntime},
		ReasonStrategy:  reason,
		ExploreStrategy: testExplore{},
		Verifier:        verifier,
	}
}

type storeFactory struct {
	name string
	open func(t *testing.T) engine.V5Store
}

func storeFactories() []storeFactory {
	return []storeFactory{
		{name: "v5", open: func(t *testing.T) engine.V5Store {
			store, err := sqlite.OpenV5Fresh(filepath.Join(t.TempDir(), "harness.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			return store
		}},
		{name: "bridge", open: func(t *testing.T) engine.V5Store {
			store, err := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			return sqlite.NewV4Bridge(store)
		}},
	}
}

func newRunner(t *testing.T, store engine.V5Store) *kernel.Runner {
	t.Helper()
	runner, err := kernel.New(kernel.Config{
		Store: store, Clock: &testClock{}, IDs: &testIDs{}, Secrets: testSecrets{},
		Workspace: runtime.OSWorkspace{}, WorkspaceRoot: t.TempDir(),
		CloseTimeout: 2 * time.Second, FinalizeTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func TestKernelLifecycleSucceedsOnlyFromVerifier(t *testing.T) {
	for _, factory := range storeFactories() {
		t.Run(factory.name, func(t *testing.T) {
			store := factory.open(t)
			runner := newRunner(t, store)
			verifier := &testVerifier{}
			result, err := runner.Run(context.Background(), testSpec(&scriptedReason{}, verifier))
			if err != nil {
				t.Fatal(err)
			}
			if result.Phase != core.PhaseTerminal || result.Outcome != core.OutcomeSucceeded {
				t.Fatalf("result = %+v", result)
			}
			if result.Verdict == nil || !result.Verdict.Success || result.Verdict.Authority != core.VerdictAuthorityVerifier {
				t.Fatalf("verdict = %+v", result.Verdict)
			}
			if verifier.count() != 1 {
				t.Fatalf("verifier calls = %d, want 1", verifier.count())
			}
			if len(result.Claims) != 3 || len(result.Intents) != 1 || len(result.Attempts) != 1 || len(result.Observations) != 1 || len(result.Artifacts) != 1 {
				t.Fatalf("board projection = claims %d intents %d attempts %d observations %d artifacts %d",
					len(result.Claims), len(result.Intents), len(result.Attempts), len(result.Observations), len(result.Artifacts))
			}
			if result.Usage.TotalTokens == 0 || result.Usage.ModelCalls != 3 {
				t.Fatalf("usage = %+v, want single accounting for two turns", result.Usage)
			}
			ctx := context.Background()
			if err := store.VerifyChain(ctx, result.RunID); err != nil {
				t.Fatal(err)
			}
			board, err := store.Board(ctx, result.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if board.Revision != 10 {
				t.Fatalf("board revision = %d, want 10", board.Revision)
			}
			verdict, err := store.Verdict(ctx, result.RunID)
			if err != nil || verdict == nil || !verdict.Success {
				t.Fatalf("stored verdict = %+v, err = %v", verdict, err)
			}
			if _, err := runner.Resume(ctx, result.RunID, testSpec(&scriptedReason{}, verifier)); err == nil {
				t.Fatal("terminal run accepted for resume")
			}
			if err := runner.Close(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestKernelVerifierFailureIsNotSuccess(t *testing.T) {
	for _, factory := range storeFactories() {
		t.Run(factory.name, func(t *testing.T) {
			runner := newRunner(t, factory.open(t))
			verifier := &testVerifier{fn: func(context.Context, core.VerifyRequest) (core.Verdict, error) {
				return core.Verdict{Success: false, Message: "not confirmed"}, nil
			}}
			result, err := runner.Run(context.Background(), testSpec(&scriptedReason{}, verifier))
			if err != nil {
				t.Fatal(err)
			}
			if result.Outcome != core.OutcomeFailed || result.Verdict == nil || result.Verdict.Success {
				t.Fatalf("result = %+v", result)
			}
			_ = runner.Close(context.Background())
		})
	}
}

func TestKernelVerifierPanicFailsClosed(t *testing.T) {
	runner := newRunner(t, storeFactories()[0].open(t))
	verifier := &testVerifier{fn: func(context.Context, core.VerifyRequest) (core.Verdict, error) {
		panic("verifier exploded")
	}}
	result, err := runner.Run(context.Background(), testSpec(&scriptedReason{}, verifier))
	if err == nil || !strings.Contains(err.Error(), "panic: verifier exploded") {
		t.Fatalf("err = %v, want verifier panic", err)
	}
	if result.Outcome != core.OutcomeFailed {
		t.Fatalf("outcome = %q, want failed", result.Outcome)
	}
	_ = runner.Close(context.Background())
}

func TestKernelStagnationNeverVerifies(t *testing.T) {
	runner := newRunner(t, storeFactories()[0].open(t))
	verifier := &testVerifier{}
	result, err := runner.Run(context.Background(), testSpec(&scriptedReason{zero: true}, verifier))
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != core.OutcomeStagnated {
		t.Fatalf("outcome = %q, want stagnated", result.Outcome)
	}
	if verifier.count() != 0 {
		t.Fatalf("verifier called %d times during stagnation", verifier.count())
	}
	_ = runner.Close(context.Background())
}

func TestKernelCancelFinalizesCancelled(t *testing.T) {
	factory := storeFactories()[0]
	store := factory.open(t)
	runner := newRunner(t, store)
	spec := testSpec(&scriptedReason{delay: time.Second}, &testVerifier{})
	run, err := runner.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case record := <-run.Events():
			if record.Type == core.RecordReasonStarted {
				_ = run.Cancel(context.Background())
				result, waitErr := run.Wait(context.Background())
				if !errors.Is(waitErr, context.Canceled) {
					t.Fatalf("wait err = %v, want context canceled", waitErr)
				}
				if result.Outcome != core.OutcomeCancelled {
					t.Fatalf("outcome = %q, want cancelled", result.Outcome)
				}
				state, err := store.State(context.Background(), run.ID)
				if err != nil || state.Phase != core.PhaseTerminal || state.Outcome != core.OutcomeCancelled {
					t.Fatalf("state = %+v, err = %v", state, err)
				}
				_ = runner.Close(context.Background())
				return
			}
		case <-deadline:
			t.Fatal("reason.started not observed")
		}
	}
}

// seedRunningRun writes a crashed Run whose intent is claimed by an in-flight
// attempt. Resume must normalize both before continuing.
func seedRunningRun(t *testing.T, store engine.V5Store, manifest core.Manifest) {
	t.Helper()
	ctx := context.Background()
	at := time.Unix(1_700_000_500, 0).UTC()
	redactor := core.NewRedactor()
	sealedManifest, err := redactor.SealManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := manifest.Digest()
	if err != nil {
		t.Fatal(err)
	}
	build := func(start uint64, prev string, items ...struct {
		typ     core.RecordType
		payload any
	}) ([]core.Record, string) {
		recs := make([]core.Record, 0, len(items))
		for i, item := range items {
			record, err := core.NewRecord(manifest.RunID, start+uint64(i), item.typ, at, item.payload, prev)
			if err != nil {
				t.Fatal(err)
			}
			recs = append(recs, record)
			prev = record.Hash
		}
		return recs, prev
	}
	created, _ := build(1, digest, struct {
		typ     core.RecordType
		payload any
	}{core.RecordRunCreated, core.RunCreatedRecord{TaskID: manifest.Task.ID, TargetID: manifest.Target.ID, ManifestDigest: digest, ReasonerID: manifest.Reasoner.ID, ExecutorID: manifest.Executor.ID}})
	first, err := redactor.SealBatch(core.Batch{RunID: manifest.RunID, ExpectedHead: 0, State: &core.State{RunID: manifest.RunID, Phase: core.PhaseQueued}, Records: created})
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Create(ctx, sealedManifest, first)
	if err != nil {
		t.Fatal(err)
	}
	origin := core.Claim{ID: "origin", RunID: manifest.RunID, TaskID: manifest.Task.ID, Kind: core.ClaimOrigin, Description: manifest.Task.Objective, CreatedAt: at}
	goal := core.Claim{ID: "goal", RunID: manifest.RunID, TaskID: manifest.Task.ID, Kind: core.ClaimGoal, Description: manifest.Task.Objective, CreatedAt: at}
	intent := core.Intent{ID: "intent-1", RunID: manifest.RunID, FromClaimIDs: []string{"origin"}, Description: "probe", State: core.IntentOpen, CreatedAt: at}
	attempt := core.IntentAttempt{ID: "attempt-1", RunID: manifest.RunID, IntentID: intent.ID, SessionID: "executor", Phase: core.IntentAttemptRunning, StartedAt: at}
	phase1, _ := build(state.HeadSeq+1, state.HeadHash,
		struct {
			typ     core.RecordType
			payload any
		}{core.RecordRunPhase, core.RunPhaseRecord{From: core.PhaseQueued, To: core.PhaseProvisioning, At: at}},
		struct {
			typ     core.RecordType
			payload any
		}{core.RecordClaimCreated, core.ClaimCreatedRecord{Claim: origin}},
	)
	provisioning := core.State{RunID: manifest.RunID, Phase: core.PhaseProvisioning}
	batch, err := redactor.SealBatch(core.Batch{RunID: manifest.RunID, ExpectedHead: state.HeadSeq, State: &provisioning, BoardRevision: 1, Records: phase1})
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.Append(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	phase2, _ := build(result.State.HeadSeq+1, result.State.HeadHash,
		struct {
			typ     core.RecordType
			payload any
		}{core.RecordRunPhase, core.RunPhaseRecord{From: core.PhaseProvisioning, To: core.PhaseRunning, At: at}},
		struct {
			typ     core.RecordType
			payload any
		}{core.RecordClaimCreated, core.ClaimCreatedRecord{Claim: goal}},
		struct {
			typ     core.RecordType
			payload any
		}{core.RecordIntentProposed, core.IntentProposedRecord{Intent: intent}},
		struct {
			typ     core.RecordType
			payload any
		}{core.RecordIntentClaimed, core.IntentClaimedRecord{IntentID: intent.ID, ExecutorID: "executor", At: at}},
		struct {
			typ     core.RecordType
			payload any
		}{core.RecordAttemptStarted, core.AttemptStartedRecord{Attempt: attempt}},
	)
	running := core.State{RunID: manifest.RunID, Phase: core.PhaseRunning}
	batch, err = redactor.SealBatch(core.Batch{RunID: manifest.RunID, ExpectedHead: result.State.HeadSeq, State: &running, BoardRevision: 5, Records: phase2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, batch); err != nil {
		t.Fatal(err)
	}
}

func TestKernelResumeNormalizesAndContinues(t *testing.T) {
	for _, factory := range storeFactories() {
		t.Run(factory.name, func(t *testing.T) {
			store := factory.open(t)
			runner := newRunner(t, store)
			spec := testSpec(&scriptedReason{goalFirst: true}, &testVerifier{})
			manifest := spec.Manifest("run-1", time.Unix(1_700_000_500, 0).UTC())
			seedRunningRun(t, store, manifest)
			run, err := runner.Resume(context.Background(), "run-1", spec)
			if err != nil {
				t.Fatal(err)
			}
			result, err := run.Wait(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if result.Outcome != core.OutcomeSucceeded {
				t.Fatalf("result = %+v", result)
			}
			events, err := store.Events(context.Background(), "run-1", 0)
			if err != nil {
				t.Fatal(err)
			}
			resumed := false
			for _, record := range events {
				if record.Type == core.RecordRunResumed {
					resumed = true
					payload, err := core.DecodeRecord(record)
					if err != nil {
						t.Fatal(err)
					}
					if len(payload.(core.RunResumedRecord).Normalizations) == 0 {
						t.Fatal("run.resumed did not carry normalization")
					}
				}
			}
			if !resumed {
				t.Fatal("resume did not append run.resumed")
			}
			board, err := store.Board(context.Background(), "run-1")
			if err != nil {
				t.Fatal(err)
			}
			if len(board.Attempts) != 2 {
				t.Fatalf("attempts = %+v, want the interrupted attempt plus the new one", board.Attempts)
			}
			if board.Attempts[0].Phase != core.IntentAttemptInterrupted {
				t.Fatalf("attempt 0 phase = %q, want interrupted", board.Attempts[0].Phase)
			}
			if board.Attempts[1].Phase != core.IntentAttemptConcluded {
				t.Fatalf("attempt 1 phase = %q, want concluded", board.Attempts[1].Phase)
			}
			if len(board.Intents) != 1 || board.Intents[0].State != core.IntentConcluded {
				t.Fatalf("intents = %+v", board.Intents)
			}
			if err := store.VerifyChain(context.Background(), "run-1"); err != nil {
				t.Fatal(err)
			}
			_ = runner.Close(context.Background())
		})
	}
}

type failingStore struct {
	engine.V5Store
	mu     sync.Mutex
	count  int
	failAt int
	err    error
}

func (s *failingStore) Append(ctx context.Context, batch core.SealedBatch) (core.AppendResult, error) {
	s.mu.Lock()
	s.count++
	fail := s.count == s.failAt
	s.mu.Unlock()
	if fail {
		return core.AppendResult{}, s.err
	}
	return s.V5Store.Append(ctx, batch)
}

func TestKernelSessionPanicFailsClosed(t *testing.T) {
	runner := newRunner(t, storeFactories()[0].open(t))
	spec := testSpec(&scriptedReason{}, &testVerifier{})
	panicRuntime := &fakeRuntime{new: func(engine.RuntimeRequest) engine.AgentSession {
		return &fakeSession{events: make(chan engine.AgentEvent, 4), prompt: func(context.Context, string, int) error {
			panic("executor exploded")
		}}
	}}
	spec.Executor.Runtime = panicRuntime
	result, err := runner.Run(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "panic: executor exploded") {
		t.Fatalf("err = %v, want executor panic", err)
	}
	if result.Outcome != core.OutcomeFailed {
		t.Fatalf("outcome = %q, want failed", result.Outcome)
	}
	_ = runner.Close(context.Background())
}

func TestKernelRunDeadlineTimesOut(t *testing.T) {
	runner := newRunner(t, storeFactories()[0].open(t))
	spec := testSpec(&scriptedReason{delay: time.Second}, &testVerifier{})
	spec.Policy.Budget.WallTime = 100 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := runner.Run(ctx, spec)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want run deadline", err)
	}
	if result.Outcome != core.OutcomeTimedOut {
		t.Fatalf("outcome = %q, want timed_out", result.Outcome)
	}
	_ = runner.Close(context.Background())
}

func TestKernelAppendFailureFailsClosed(t *testing.T) {
	inner := storeFactories()[0].open(t)
	injected := errors.New("injected append failure")
	store := &failingStore{V5Store: inner, failAt: 3, err: injected}
	runner := newRunner(t, store)
	result, err := runner.Run(context.Background(), testSpec(&scriptedReason{}, &testVerifier{}))
	if err == nil || !strings.Contains(err.Error(), "injected append failure") {
		t.Fatalf("err = %v, want injected failure", err)
	}
	if result.Outcome != core.OutcomeFailed {
		t.Fatalf("outcome = %q, want failed", result.Outcome)
	}
	state, stateErr := inner.State(context.Background(), result.RunID)
	if stateErr != nil || state.Phase != core.PhaseTerminal || state.Outcome != core.OutcomeFailed {
		t.Fatalf("state = %+v, err = %v", state, stateErr)
	}
	if err := inner.VerifyChain(context.Background(), result.RunID); err != nil {
		t.Fatal(err)
	}
	_ = runner.Close(context.Background())
}

func TestKernelResumeRejectsMismatchedBindings(t *testing.T) {
	store := storeFactories()[0].open(t)
	runner := newRunner(t, store)
	spec := testSpec(&scriptedReason{goalFirst: true}, &testVerifier{})
	seedRunningRun(t, store, spec.Manifest("run-1", time.Unix(1_700_000_500, 0).UTC()))
	bad := spec
	bad.Reasoner.ID = "someone-else"
	if _, err := runner.Resume(context.Background(), "run-1", bad); err == nil || !strings.Contains(err.Error(), "reasoner") {
		t.Fatalf("err = %v, want binding mismatch", err)
	}
	_ = runner.Close(context.Background())
}

func TestKernelResumeUsesPersistedManifest(t *testing.T) {
	store := storeFactories()[0].open(t)
	runner := newRunner(t, store)
	var gotTask string
	var gotTarget string
	verifier := &testVerifier{fn: func(_ context.Context, req core.VerifyRequest) (core.Verdict, error) {
		gotTask = req.Task.ID
		gotTarget = req.Target.TargetID
		return core.Verdict{Success: true}, nil
	}}
	spec := testSpec(&scriptedReason{goalFirst: true}, verifier)
	seedRunningRun(t, store, spec.Manifest("run-1", time.Unix(1_700_000_500, 0).UTC()))
	overridden := spec
	overridden.Task = core.Task{ID: "caller-task", Objective: "caller override"}
	overridden.Target = core.TargetSpec{ID: "caller-target", Isolation: core.IsolationCallerManaged}
	run, err := runner.Resume(context.Background(), "run-1", overridden)
	if err != nil {
		t.Fatal(err)
	}
	result, err := run.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != core.OutcomeSucceeded {
		t.Fatalf("result = %+v", result)
	}
	if gotTask != "task-1" || gotTarget != "target-1" {
		t.Fatalf("verifier saw task %q target %q, want the persisted manifest", gotTask, gotTarget)
	}
	_ = runner.Close(context.Background())
}

func TestKernelConcurrentResumeIsExclusive(t *testing.T) {
	store := storeFactories()[0].open(t)
	runner := newRunner(t, store)
	spec := testSpec(&scriptedReason{delay: 500 * time.Millisecond}, &testVerifier{})
	seedRunningRun(t, store, spec.Manifest("run-1", time.Unix(1_700_000_500, 0).UTC()))
	first, err := runner.Resume(context.Background(), "run-1", spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Resume(context.Background(), "run-1", spec); err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("second resume err = %v, want already active", err)
	}
	_ = first.Cancel(context.Background())
	_, _ = first.Wait(context.Background())
	_ = runner.Close(context.Background())
}

type flakyExplore struct {
	mu    sync.Mutex
	calls int
}

func (f *flakyExplore) ExecutePrompt(core.ExploreRequest) (string, error)  { return "explore", nil }
func (f *flakyExplore) ConcludePrompt(core.ExploreRequest) (string, error) { return "conclude", nil }
func (f *flakyExplore) Parse(req core.ExploreRequest, turn core.TurnResult) (core.AttemptReport, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.mu.Unlock()
	// The execute and conclude parse both fail for the first attempt; the
	// retried attempt parses normally.
	if call <= 2 {
		return core.AttemptReport{}, errors.New("unusable report")
	}
	return testExplore{}.Parse(req, turn)
}

func TestKernelRetryReopensIntent(t *testing.T) {
	store := storeFactories()[0].open(t)
	runner := newRunner(t, store)
	spec := testSpec(&scriptedReason{}, &testVerifier{})
	spec.ExploreStrategy = &flakyExplore{}
	spec.Policy.Exploration.MaxAttemptsPerIntent = 2
	result, err := runner.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != core.OutcomeSucceeded {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Attempts) != 2 {
		t.Fatalf("attempts = %+v, want a failed attempt and a retried one", result.Attempts)
	}
	if result.Attempts[0].Phase != core.IntentAttemptFailed || result.Attempts[1].Phase != core.IntentAttemptConcluded {
		t.Fatalf("attempt phases = %q (%s), %q (%s)", result.Attempts[0].Phase, result.Attempts[0].Error, result.Attempts[1].Phase, result.Attempts[1].Error)
	}
	events, err := store.Events(context.Background(), result.RunID, 0)
	if err != nil {
		t.Fatal(err)
	}
	reopened := false
	for _, record := range events {
		if record.Type == core.RecordIntentReopened {
			reopened = true
		}
	}
	if !reopened {
		t.Fatal("retry did not append intent.reopened")
	}
	if err := store.VerifyChain(context.Background(), result.RunID); err != nil {
		t.Fatal(err)
	}
	_ = runner.Close(context.Background())
}
