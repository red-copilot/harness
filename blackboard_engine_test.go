package harness_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

type recordingVerifier struct{ calls int }

func (v *recordingVerifier) Verify(context.Context, harness.VerifyRequest) (harness.Verdict, error) {
	v.calls++
	return harness.Verdict{Success: true}, nil
}

type singleReasonStrategy struct {
	mu    sync.Mutex
	calls int
}

func (s *singleReasonStrategy) Prompt(harness.ReasonRequest) (string, error) { return "reason", nil }
func (s *singleReasonStrategy) Parse(req harness.ReasonRequest, _ harness.TurnResult) (harness.ReasonReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls == 1 {
		return harness.ReasonReport{BoardRevision: req.BoardRevision, Intents: []harness.IntentProposal{{From: []string{"origin"}, Description: "first"}}}, nil
	}
	return harness.ReasonReport{BoardRevision: req.BoardRevision}, nil
}

type oneIntentReasonStrategy struct{}

func (oneIntentReasonStrategy) Prompt(harness.ReasonRequest) (string, error) { return "reason", nil }
func (oneIntentReasonStrategy) Parse(req harness.ReasonRequest, _ harness.TurnResult) (harness.ReasonReport, error) {
	return harness.ReasonReport{BoardRevision: req.BoardRevision, Intents: []harness.IntentProposal{{From: []string{"origin"}, Description: "probe"}}}, nil
}

func TestBlackboardEngineRunsReasonAndOneAttempt(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.Verifier = fakeVerifier{verdict: harness.Verdict{Success: true, Message: "verified"}}
	spec.ReasonStrategy = &singleReasonStrategy{}
	spec.Executor.ID = "executor-1"
	spec.Policy.Exploration.MaxTotalIntents = 4
	spec.Policy.Exploration.MaxAttemptsPerIntent = 1
	spec.Policy.Exploration.StagnationRounds = 1
	spec.Policy.Exploration.MaxWallTimePerAttempt = time.Second
	spec.Policy.Exploration.ConcludeTimeout = 100 * time.Millisecond
	spec.Reasoner.Runtime = &fakeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		return promptReportSession(req.WorkspaceDir, `{"board_revision":1,"intents":[{"from":["origin"],"description":"probe"}]}`)
	}}
	spec.Executor.Runtime = &fakeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		return promptReportSessionWithEvidence(req.WorkspaceDir, evidenceBackedReport("negative", "test"), map[string]string{"evidence.txt": "test"})
	}}

	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Claims) < 3 {
		t.Fatalf("claims = %+v, want origin, goal and an evidenced negative", result.Claims)
	}
	if len(result.Intents) != 1 {
		t.Fatalf("intents = %+v, want one admitted intent", result.Intents)
	}
	if len(result.IntentAttempts) != 1 {
		t.Fatalf("attempts = %+v, want one attempt", result.IntentAttempts)
	}
}

func TestBlackboardEngineStartsReasonWithoutBootstrap(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.Verifier = fakeVerifier{verdict: harness.Verdict{Success: false}}
	spec.ReasonStrategy = &singleReasonStrategy{}
	spec.Policy.Exploration.MaxAttemptsPerIntent = 1
	spec.Policy.Exploration.StagnationRounds = 1
	spec.Reasoner.Runtime = &fakeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		return promptReportSession(req.WorkspaceDir, `{"board_revision":1,"intents":[{"from":["origin"],"description":"probe"}]}`)
	}}
	spec.Executor.Runtime = &fakeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		return promptReportSessionWithEvidence(req.WorkspaceDir, evidenceBackedReport("negative", "explored"), map[string]string{"evidence.txt": "explored"})
	}}

	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Intents) == 0 {
		t.Fatal("reasoner did not create an Intent")
	}
}

func TestBlackboardEngineStopsAtMaxAttemptsPerIntent(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.Verifier = fakeVerifier{verdict: harness.Verdict{Success: false}}
	spec.ReasonStrategy = oneIntentReasonStrategy{}
	spec.Executor.ID = "executor-1"
	spec.Policy.Exploration.MaxTotalIntents = 1
	spec.Policy.Exploration.MaxAttemptsPerIntent = 2
	spec.Policy.Exploration.StagnationRounds = 3
	spec.Policy.Exploration.MaxWallTimePerAttempt = time.Second
	spec.Policy.Exploration.ConcludeTimeout = 100 * time.Millisecond
	spec.Reasoner.Runtime = &fakeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		return promptReportSession(req.WorkspaceDir, `{"board_revision":1,"intents":[{"from":["origin"],"description":"probe"}]}`)
	}}
	executor := &fakeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		return promptReportSession(req.WorkspaceDir, "not json")
	}}
	spec.Executor.Runtime = executor
	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.IntentAttempts) > 2 {
		t.Fatalf("attempts = %d, want <= 2", len(result.IntentAttempts))
	}
	if len(result.Intents) == 0 || result.Intents[0].State != harness.IntentAbandoned {
		t.Fatalf("intents = %+v, want abandoned intent", result.Intents)
	}
}

func TestBlackboardEngineStopsOnEvidenceValidationFailure(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.Verifier = fakeVerifier{verdict: harness.Verdict{Success: false}}
	spec.ReasonStrategy = oneIntentReasonStrategy{}
	spec.Executor.ID = "executor-1"
	spec.Policy.Exploration.MaxTotalIntents = 1
	spec.Policy.Exploration.MaxAttemptsPerIntent = 2
	spec.Policy.Exploration.StagnationRounds = 3
	spec.Policy.Exploration.MaxWallTimePerAttempt = time.Second
	spec.Policy.Exploration.ConcludeTimeout = 100 * time.Millisecond
	spec.Reasoner.Runtime = &fakeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		return promptReportSession(req.WorkspaceDir, `{"board_revision":1,"intents":[{"from":["origin"],"description":"probe"}]}`)
	}}
	bad := `{"phase":"concluded","claim":{"kind":"finding","description":"found","observation_refs":["obs-1"]},"observations":[{"local_id":"obs-1","kind":"test","summary":"result","target":"target-1","evidence_files":["proof.txt"]}]}`
	executor := &fakeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		return promptReportSession(req.WorkspaceDir, bad)
	}}
	spec.Executor.Runtime = executor
	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.IntentAttempts) > 2 {
		t.Fatalf("attempts = %d, want <= 2", len(result.IntentAttempts))
	}
	if len(result.Intents) == 0 || result.Intents[0].State != harness.IntentAbandoned {
		t.Fatalf("intents = %+v, want abandoned intent", result.Intents)
	}
}

func TestArtifactStoreFailureTerminatesRunWithoutVerification(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	artifactErr := errors.New("artifact disk full")
	spec.Artifacts = &fakeArtifactStore{fail: artifactErr}
	verifier := &recordingVerifier{}
	spec.Verifier = verifier
	spec.ReasonStrategy = oneIntentReasonStrategy{}
	spec.Reasoner.Runtime = &fakeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		return promptReportSession(req.WorkspaceDir, `{"board_revision":1,"intents":[{"from":["origin"],"description":"probe"}]}`)
	}}
	finding := `{"phase":"concluded","claim":{"kind":"finding","description":"confirmed","observation_refs":["obs-1"]},"observations":[{"local_id":"obs-1","kind":"test","summary":"result","target":"target-1","evidence_files":["proof.txt"]}]}`
	executor := &fakeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		if err := os.WriteFile(filepath.Join(req.WorkspaceDir, "proof.txt"), []byte("proof"), 0o600); err != nil {
			t.Errorf("write evidence: %v", err)
		}
		return promptReportSession(req.WorkspaceDir, finding)
	}}
	spec.Executor.Runtime = executor

	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "artifact store") {
		t.Fatalf("err = %v, want artifact store failure", err)
	}
	if verifier.calls != 0 {
		t.Fatalf("verifier calls = %d, want 0 after artifact store failure", verifier.calls)
	}
	if result.Outcome != harness.RunOutcomeFailed {
		t.Fatalf("outcome = %q, want failed", result.Outcome)
	}
}
