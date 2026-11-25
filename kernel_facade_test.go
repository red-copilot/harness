package harness_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/core"
	"github.com/red-copilot/harness/runtime"
	"github.com/red-copilot/harness/store/sqlite"
)

type facadeClock struct {
	mu   sync.Mutex
	tick int64
}

func (c *facadeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tick++
	return time.Unix(1_700_010_000, c.tick*int64(time.Millisecond)).UTC()
}

type facadeIDs struct {
	n atomic.Uint64
}

func (i *facadeIDs) NewRunID() (string, error) {
	return "facade-run-" + string(rune('0'+i.n.Add(1))), nil
}

type facadeSecrets struct{}

func (facadeSecrets) LookupEnv(name string) (string, bool) { return os.LookupEnv(name) }

type facadeSession struct {
	events  chan harness.AgentEvent
	mu      sync.Mutex
	prompts int
	write   func(context.Context) error
}

func (s *facadeSession) Prompt(ctx context.Context, _ string) error {
	s.mu.Lock()
	s.prompts++
	s.mu.Unlock()
	if s.write != nil {
		if err := s.write(ctx); err != nil {
			return err
		}
	}
	select {
	case s.events <- harness.AgentEvent{Kind: harness.AgentEventSettled}:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}
func (s *facadeSession) Reset(context.Context) error       { return nil }
func (s *facadeSession) Events() <-chan harness.AgentEvent { return s.events }
func (s *facadeSession) Logs() <-chan harness.LogEvent     { return nil }
func (s *facadeSession) Abort(context.Context) error       { return nil }
func (s *facadeSession) Close(context.Context) error       { return nil }

func (s *facadeSession) Usage() harness.Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return harness.Usage{InputTokens: 10 * s.prompts, OutputTokens: 5 * s.prompts, TotalTokens: 15 * s.prompts, ModelCalls: s.prompts}
}

type facadeRuntime struct {
	new func(harness.RuntimeRequest) harness.AgentSession
}

func (r *facadeRuntime) NewSession(_ context.Context, req harness.RuntimeRequest) (harness.AgentSession, error) {
	return r.new(req), nil
}

type facadeReason struct {
	mu    sync.Mutex
	calls int
}

func (f *facadeReason) Prompt(harness.ReasonRequest) (string, error) { return "reason", nil }
func (f *facadeReason) Parse(req harness.ReasonRequest, _ harness.TurnResult) (harness.ReasonReport, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.mu.Unlock()
	if call > 1 {
		return harness.ReasonReport{BoardRevision: req.BoardRevision, GoalCandidate: &harness.GoalCandidate{
			From: []string{"origin"}, Description: "done",
		}}, nil
	}
	return harness.ReasonReport{BoardRevision: req.BoardRevision, Intents: []harness.IntentProposal{{
		From: []string{"origin"}, Description: "probe",
	}}}, nil
}

type facadeExplore struct{}

func (facadeExplore) ExecutePrompt(harness.ExploreRequest) (string, error)  { return "explore", nil }
func (facadeExplore) ConcludePrompt(harness.ExploreRequest) (string, error) { return "conclude", nil }
func (facadeExplore) Parse(req harness.ExploreRequest, _ harness.TurnResult) (harness.AttemptReport, error) {
	return harness.AttemptReport{
		IntentID: req.Intent.ID, Phase: harness.IntentAttemptConcluded,
		Claim: &harness.ClaimProposal{Kind: harness.ClaimNegative, Description: "none", ObservationRefs: []string{"obs"}},
		Observations: []harness.ObservationProposal{{
			LocalID: "obs", Kind: "test", Summary: "none", Target: "target-1", EvidenceFiles: []string{"evidence.txt"},
		}},
	}, nil
}

type facadeVerifier struct{ success bool }

func (v facadeVerifier) Verify(context.Context, harness.VerifyRequest) (harness.Verdict, error) {
	return harness.Verdict{Success: v.success, Score: 80}, nil
}

func facadeSpec(t *testing.T, success bool) harness.RunSpec {
	t.Helper()
	reasonRuntime := &facadeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		return &facadeSession{events: make(chan harness.AgentEvent, 8), write: func(ctx context.Context) error {
			if err := os.MkdirAll(req.WorkspaceDir, 0o700); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(req.WorkspaceDir, "report.json"), []byte(`{}`), 0o600)
		}}
	}}
	executorRuntime := &facadeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		return &facadeSession{events: make(chan harness.AgentEvent, 8), write: func(context.Context) error {
			if err := os.MkdirAll(req.WorkspaceDir, 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(req.WorkspaceDir, "evidence.txt"), []byte("no signal"), 0o600); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(req.WorkspaceDir, "report.json"), []byte(`{}`), 0o600)
		}}
	}}
	policy := harness.DefaultRunPolicy()
	policy.Exploration.EnginePollInterval = 5 * time.Millisecond
	policy.Exploration.MaxAttemptsPerIntent = 1
	policy.Exploration.MaxReasonerWallTime = 2 * time.Second
	policy.Exploration.MaxWallTimePerAttempt = 2 * time.Second
	policy.Exploration.DrainTimeout = 500 * time.Millisecond
	return harness.RunSpec{
		Task:            harness.Task{ID: "task-1", Objective: "facade"},
		Target:          harness.TargetSpec{ID: "target-1", Addresses: []string{"10.0.0.1:80"}, Isolation: harness.IsolationCallerManaged},
		Policy:          policy,
		Reasoner:        harness.AgentSpec{ID: "reasoner", Runtime: reasonRuntime},
		Executor:        harness.AgentSpec{ID: "executor", Runtime: executorRuntime},
		ReasonStrategy:  &facadeReason{},
		ExploreStrategy: facadeExplore{},
		Verifier:        facadeVerifier{success: success},
	}
}

func facadeRunner(t *testing.T, store harness.Store) *harness.Runner {
	t.Helper()
	runner, err := harness.NewRunner(harness.Config{
		Store: store, Clock: &facadeClock{}, IDs: &facadeIDs{}, Secrets: facadeSecrets{},
		Workspace: runtime.OSWorkspace{}, WorkspaceRoot: t.TempDir(),
		CloseTimeout: 2 * time.Second, FinalizeTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func TestFacadeRunSucceedsOnlyFromVerifier(t *testing.T) {
	store, err := sqlite.OpenV5Fresh(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	runner := facadeRunner(t, store)
	result, err := runner.Run(context.Background(), facadeSpec(t, true))
	if err != nil {
		t.Fatal(err)
	}
	if result.Phase != core.PhaseTerminal || result.Outcome != core.OutcomeSucceeded {
		t.Fatalf("result = %+v", result)
	}
	if result.Verdict == nil || !result.Verdict.Success || result.Verdict.Authority != harness.VerdictAuthorityVerifier {
		t.Fatalf("verdict = %+v", result.Verdict)
	}
	if len(result.Claims) != 3 || len(result.Attempts) != 1 || len(result.Artifacts) != 1 {
		t.Fatalf("result board = %+v", result)
	}
	_ = runner.Close(context.Background())
}

func TestFacadeVerifierRejectionIsNotSuccess(t *testing.T) {
	store, err := sqlite.OpenV5Fresh(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	runner := facadeRunner(t, store)
	result, err := runner.Run(context.Background(), facadeSpec(t, false))
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != core.OutcomeFailed || result.Verdict == nil || result.Verdict.Success {
		t.Fatalf("result = %+v", result)
	}
	_ = runner.Close(context.Background())
}

func TestFacadeRejectsSpecWithoutVerifier(t *testing.T) {
	spec := facadeSpec(t, true)
	spec.Verifier = nil
	if err := harness.ValidateRunSpec(spec); err == nil || !strings.Contains(err.Error(), "verifier") {
		t.Fatalf("err = %v, want verifier required", err)
	}
}
