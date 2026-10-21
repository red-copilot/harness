package harness_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

func TestPlannerCannotCreateNonRootBranch(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.PlannerStrategy = staticPlannerStrategy{proposal: harness.PlanProposal{CreateBranches: []harness.BranchProposal{
		{ParentID: "root", Topic: "web", Goal: "web", Hypothesis: "web exposed", Target: "target-1", SuccessCondition: "found", FailureCondition: "not found"},
		{ParentID: "not-root", Topic: "bad", Goal: "bad", Hypothesis: "bad", Target: "target-1", SuccessCondition: "bad", FailureCondition: "bad"},
	}}}
	spec.ExecutorStrategy = noChildExecutorStrategy{}
	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Branches) != 2 {
		t.Fatalf("branches = %d, want root + one valid: %+v", len(result.Branches), result.Branches)
	}
	events, _ := store.ListEvents(context.Background(), result.RunID, 0)
	if !eventContains(events, "exploration.branch.rejected", `"reason":"planner_cannot_expand"`) {
		t.Fatalf("missing planner rejection: %+v", events)
	}
}

func TestExpansionRequiresLocalSuccessAndObservation(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.Policy.MaxAttemptsPerBranch = 1
	spec.ExecutorStrategy = staticExecutorStrategy{report: harness.AttemptReport{
		Status: harness.AttemptCompleted, Outcome: harness.AttemptInconclusive,
		ChildBranches: []harness.BranchProposal{{ParentID: "branch-1", Topic: "child", Goal: "child", Hypothesis: "child", Target: "target-1", SuccessCondition: "yes", FailureCondition: "no"}},
	}}
	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Branches) != 2 {
		t.Fatalf("inconclusive attempt expanded children: %+v", result.Branches)
	}
}

func TestChildLimitIsMechanical(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.Policy.MaxAttemptsPerBranch = 1
	spec.ExecutorStrategy = staticExecutorStrategy{report: harness.AttemptReport{
		Status: harness.AttemptCompleted, Outcome: harness.AttemptLocalSuccess,
		Observations: []harness.ObservationProposal{{LocalID: "obs-1", Kind: "test.result", Summary: "observed", Target: "target-1", EvidenceFiles: []string{"proof.txt"}}},
		ChildBranches: []harness.BranchProposal{
			{ParentID: "branch-1", Topic: "child.one", Goal: "one", Hypothesis: "one", Target: "target-1", SuccessCondition: "yes", FailureCondition: "no"},
			{ParentID: "branch-1", Topic: "child.two", Goal: "two", Hypothesis: "two", Target: "target-1", SuccessCondition: "yes", FailureCondition: "no"},
			{ParentID: "branch-1", Topic: "child.three", Goal: "three", Hypothesis: "three", Target: "target-1", SuccessCondition: "yes", FailureCondition: "no"},
		},
	}}
	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	var parentID string
	for _, branch := range result.Branches {
		if branch.Kind == harness.BranchHypothesis {
			parentID = branch.ID
			break
		}
	}
	children := 0
	for _, branch := range result.Branches {
		if branch.ParentID == parentID {
			children++
		}
	}
	if children != 2 {
		t.Fatalf("children = %d, want 2: %+v", children, result.Branches)
	}
}

func TestMaxActiveBranchesIsConcurrent(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.Policy.MaxActiveBranches = 2
	spec.Policy.MaxAttemptsPerBranch = 1
	spec.Executors = append(spec.Executors, harness.AgentSpec{ID: "exec-2", Runtime: spec.Executors[0].Runtime})
	spec.PlannerStrategy = staticPlannerStrategy{proposal: harness.PlanProposal{CreateBranches: []harness.BranchProposal{
		{ParentID: "root", Topic: "one", Goal: "one", Hypothesis: "one", Target: "target-1", SuccessCondition: "yes", FailureCondition: "no"},
		{ParentID: "root", Topic: "two", Goal: "two", Hypothesis: "two", Target: "target-1", SuccessCondition: "yes", FailureCondition: "no"},
	}}}
	release := make(chan struct{})
	var callsMu sync.Mutex
	calls := 0
	spec.ExecutorStrategy = blockingExecutorStrategy{onPrompt: func(req harness.AttemptRequest) {
		callsMu.Lock()
		calls++
		if calls == 2 {
			close(release)
		}
		callsMu.Unlock()
		<-release
	}}
	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(result.Attempts))
	}
}

func TestAgentEndDoesNotCompleteTurn(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.Policy.MaxAttemptsPerBranch = 1
	spec.ExecutorStrategy = agentEndExecutorStrategy{}
	spec.Verifier = fakeVerifier{verdict: harness.Verdict{Success: false}}
	spec.Executors[0].Runtime = &fakeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		session := &fakeSession{events: make(chan harness.AgentEvent, 4)}
		session.prompt = func(context.Context, string) error {
			session.events <- harness.AgentEvent{Type: "turn_end", Data: []byte(`{"type":"turn_end"}`)}
			session.events <- harness.AgentEvent{Type: "agent_end", Data: []byte(`{"type":"agent_end"}`)}
			close(session.events)
			return nil
		}
		return session
	}}
	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err != nil && !strings.Contains(err.Error(), "agent_settled") {
		t.Fatalf("run error = %v", err)
	}
	if result.Status == harness.StatusSucceeded {
		t.Fatal("agent_end established success")
	}
	for _, attempt := range result.Attempts {
		if attempt.Status == harness.AttemptCompleted {
			t.Fatalf("agent_end completed attempt: %+v", attempt)
		}
	}
}

func TestEventStreamCloseBeforeSettlementFailsAttempt(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.Policy.MaxAttemptsPerBranch = 1
	spec.ExecutorStrategy = closedExecutorStrategy{}
	spec.Verifier = fakeVerifier{verdict: harness.Verdict{Success: false}}
	spec.Executors[0].Runtime = &fakeRuntime{new: func(req harness.RuntimeRequest) harness.AgentSession {
		events := make(chan harness.AgentEvent)
		close(events)
		return &fakeSession{events: events, prompt: func(context.Context, string) error { return nil }}
	}}
	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err != nil && !strings.Contains(err.Error(), "before agent_settled") {
		t.Fatalf("run error = %v", err)
	}
	if result.Status == harness.StatusSucceeded {
		t.Fatal("closed stream established success")
	}
}

func integrationSpec(t *testing.T, store *memoryStore) harness.RunSpec {
	t.Helper()
	spec := validRunSpec()
	spec.Artifacts = &fakeArtifactStore{}
	spec.PlannerStrategy = staticPlannerStrategy{proposal: harness.PlanProposal{CreateBranches: []harness.BranchProposal{{ParentID: "root", Topic: "web", Goal: "web", Hypothesis: "web exposed", Target: "target-1", SuccessCondition: "found", FailureCondition: "not found"}}}}
	spec.ExecutorStrategy = noChildExecutorStrategy{}
	return spec
}

func newTestRunner(t *testing.T, store *memoryStore) *harness.Runner {
	t.Helper()
	runner, err := harness.NewRunner(harness.Config{Store: store, Starter: noopStarter{}, WorkspaceRoot: t.TempDir(), SkipRecovery: true, PollInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

type staticPlannerStrategy struct{ proposal harness.PlanProposal }

func (s staticPlannerStrategy) Prompt(harness.PlanRequest) (string, error) { return "plan", nil }
func (s staticPlannerStrategy) Parse(harness.PlanRequest, harness.TurnResult) (harness.PlanProposal, error) {
	return s.proposal, nil
}

type noChildExecutorStrategy struct{}

func (noChildExecutorStrategy) Prompt(harness.AttemptRequest) (string, error) { return "execute", nil }
func (noChildExecutorStrategy) Parse(harness.AttemptRequest, harness.TurnResult) (harness.AttemptReport, error) {
	return harness.AttemptReport{BranchID: "branch-1", Status: harness.AttemptCompleted, Outcome: harness.AttemptInconclusive}, nil
}

type staticExecutorStrategy struct{ report harness.AttemptReport }

func (s staticExecutorStrategy) Prompt(harness.AttemptRequest) (string, error) { return "execute", nil }
func (s staticExecutorStrategy) Parse(req harness.AttemptRequest, _ harness.TurnResult) (harness.AttemptReport, error) {
	report := s.report
	report.BranchID = req.Branch.ID
	for i := range report.ChildBranches {
		report.ChildBranches[i].ParentID = req.Branch.ID
	}
	for i := range report.Observations {
		if len(report.Observations[i].EvidenceFiles) > 0 {
			name := report.Observations[i].EvidenceFiles[0]
			_ = os.WriteFile(filepath.Join(req.WorkspaceDir, name), []byte("evidence"), 0o600)
		}
	}
	return report, nil
}

type blockingExecutorStrategy struct{ onPrompt func(harness.AttemptRequest) }

func (s blockingExecutorStrategy) Prompt(req harness.AttemptRequest) (string, error) {
	s.onPrompt(req)
	return "execute", nil
}
func (blockingExecutorStrategy) Parse(harness.AttemptRequest, harness.TurnResult) (harness.AttemptReport, error) {
	return harness.AttemptReport{Status: harness.AttemptCompleted, Outcome: harness.AttemptInconclusive}, nil
}

type agentEndExecutorStrategy struct{}

func (agentEndExecutorStrategy) Prompt(harness.AttemptRequest) (string, error) { return "execute", nil }
func (agentEndExecutorStrategy) Parse(harness.AttemptRequest, harness.TurnResult) (harness.AttemptReport, error) {
	return harness.AttemptReport{Status: harness.AttemptCompleted, Outcome: harness.AttemptInconclusive}, nil
}

type closedExecutorStrategy struct{}

func (closedExecutorStrategy) Prompt(harness.AttemptRequest) (string, error) { return "execute", nil }
func (closedExecutorStrategy) Parse(harness.AttemptRequest, harness.TurnResult) (harness.AttemptReport, error) {
	return harness.AttemptReport{Status: harness.AttemptCompleted, Outcome: harness.AttemptInconclusive}, nil
}

func eventContains(events []harness.Event, typ, data string) bool {
	for _, event := range events {
		if event.Type == typ && strings.Contains(string(event.Data), data) {
			return true
		}
	}
	return false
}

// TestBlockedAndRefutedAttemptsNeedNoEvidence: a negative finding is the
// result itself, so an attempt with no observations must not be downgraded to
// a failure just because there is nothing to archive.
func TestBlockedAndRefutedAttemptsNeedNoEvidence(t *testing.T) {
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.PlannerStrategy = staticPlannerStrategy{proposal: harness.PlanProposal{CreateBranches: []harness.BranchProposal{
		{ParentID: "root", Topic: "web.blocked", Goal: "blocked", Hypothesis: "blocked", Target: "target-1", SuccessCondition: "yes", FailureCondition: "no"},
		{ParentID: "root", Topic: "web.refuted", Goal: "refuted", Hypothesis: "refuted", Target: "target-1", SuccessCondition: "yes", FailureCondition: "no"},
	}}}
	spec.ExecutorStrategy = noEvidenceOutcomeExecutor{outcomes: map[string]harness.AttemptOutcome{
		"web.blocked": harness.AttemptBlocked,
		"web.refuted": harness.AttemptRefuted,
	}}
	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	branches, err := store.ListBranches(context.Background(), result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]harness.BranchStatus{}
	for _, branch := range branches {
		statuses[branch.Topic] = branch.Status
	}
	if statuses["web.blocked"] != harness.BranchBlocked || statuses["web.refuted"] != harness.BranchRefuted {
		t.Fatalf("branch statuses = %#v", statuses)
	}
	attempts, err := store.ListAttempts(context.Background(), result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	outcomes := map[harness.AttemptOutcome]bool{}
	for _, attempt := range attempts {
		if attempt.Status == harness.AttemptFailed {
			t.Fatalf("negative attempt was downgraded to failure: %#v", attempt)
		}
		outcomes[attempt.Outcome] = true
	}
	if !outcomes[harness.AttemptBlocked] || !outcomes[harness.AttemptRefuted] {
		t.Fatalf("attempt outcomes = %#v", outcomes)
	}
}

type noEvidenceOutcomeExecutor struct {
	outcomes map[string]harness.AttemptOutcome
}

func (noEvidenceOutcomeExecutor) Prompt(harness.AttemptRequest) (string, error) {
	return "execute", nil
}
func (e noEvidenceOutcomeExecutor) Parse(req harness.AttemptRequest, _ harness.TurnResult) (harness.AttemptReport, error) {
	return harness.AttemptReport{BranchID: req.Branch.ID, Status: harness.AttemptCompleted, Outcome: e.outcomes[req.Branch.Topic]}, nil
}
