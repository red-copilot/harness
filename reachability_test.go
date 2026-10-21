package harness_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/store/sqlite"
)

// observedStates accumulates every declared state that production transitions
// actually produced during the scenario battery below.
type observedStates struct {
	statuses map[harness.Status]bool
	branches map[harness.BranchStatus]bool
	attempts map[harness.AttemptStatus]bool
	outcomes map[harness.AttemptOutcome]bool
	trusts   map[harness.KnowledgeTrust]bool
}

func newObservedStates() *observedStates {
	return &observedStates{
		statuses: map[harness.Status]bool{},
		branches: map[harness.BranchStatus]bool{},
		attempts: map[harness.AttemptStatus]bool{},
		outcomes: map[harness.AttemptOutcome]bool{},
		trusts:   map[harness.KnowledgeTrust]bool{},
	}
}

func (o *observedStates) collect(t *testing.T, store *memoryStore, result harness.Result) {
	t.Helper()
	ctx := context.Background()
	o.statuses[result.Status] = true
	runs, err := store.ListRuns(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range runs {
		o.statuses[record.Status] = true
	}
	for _, status := range store.statusLog {
		o.statuses[status] = true
	}
	branches, err := store.ListBranches(ctx, result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, branch := range branches {
		o.branches[branch.Status] = true
	}
	attempts, err := store.ListAttempts(ctx, result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, attempt := range attempts {
		o.attempts[attempt.Status] = true
		o.outcomes[attempt.Outcome] = true
	}
	events, err := store.ListEvents(ctx, result.RunID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		switch event.Type {
		case "run.status":
			var payload struct {
				Status string `json:"status"`
			}
			if json.Unmarshal(event.Data, &payload) == nil {
				o.statuses[harness.Status(payload.Status)] = true
			}
		case "knowledge.promoted", "knowledge.contested", "knowledge.refuted":
			var item harness.Knowledge
			if json.Unmarshal(event.Data, &item) == nil && item.Trust != "" {
				o.trusts[item.Trust] = true
			}
		}
	}
	for _, item := range result.Knowledge {
		o.trusts[item.Trust] = true
	}
}

// TestDeclaredStatesAreReachable drives every declared lifecycle constant
// through a production transition and fails when a declared state has no
// writer. States without a writer must be deleted, not hidden in a fixture.
func TestDeclaredStatesAreReachable(t *testing.T) {
	observed := newObservedStates()

	// Success with a contested pair, a review branch and a verdict.
	store := newMemoryStore()
	spec := integrationSpec(t, store)
	spec.PlannerStrategy = conflictReviewPlanner{}
	spec.ExecutorStrategy = conflictReviewExecutor{}
	result, err := newTestRunner(t, store).Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	observed.collect(t, store, result)

	// A rejecting verdict is its own failure path.
	store = newMemoryStore()
	spec = integrationSpec(t, store)
	spec.Verifier = fakeVerifier{verdict: harness.Verdict{Success: false, Message: "not solved"}}
	result, err = newTestRunner(t, store).Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	observed.collect(t, store, result)

	// Cancellation of a live run.
	store = newMemoryStore()
	spec = integrationSpec(t, store)
	started := make(chan struct{}, 1)
	spec.Executors[0].Runtime = &fakeRuntime{new: func(harness.RuntimeRequest) harness.AgentSession {
		return &fakeSession{events: make(chan harness.AgentEvent), prompt: func(ctx context.Context, _ string) error {
			select {
			case started <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return ctx.Err()
		}}
	}}
	runner := newTestRunner(t, store)
	run, err := runner.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("executor session never started")
	}
	if err := run.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err = run.Wait(context.Background())
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	observed.collect(t, store, result)

	// Wall-clock budget exhaustion.
	store = newMemoryStore()
	spec = integrationSpec(t, store)
	spec.Budget = harness.Budget{WallTime: 50 * time.Millisecond}
	spec.Executors[0].Runtime = &fakeRuntime{new: func(harness.RuntimeRequest) harness.AgentSession {
		return &fakeSession{events: make(chan harness.AgentEvent), prompt: func(ctx context.Context, _ string) error {
			<-ctx.Done()
			return ctx.Err()
		}}
	}}
	result, _ = newTestRunner(t, store).Run(context.Background(), spec)
	observed.collect(t, store, result)

	// Stagnation: a failing attempt makes no progress and the backend never
	// produces an authoritative completion, so the run ends stagnated.
	store = newMemoryStore()
	spec = integrationSpec(t, store)
	spec.Verifier = nil
	attachBackend(&spec, &fakeBackend{})
	spec.Policy.StagnationRounds = 1
	spec.PlannerStrategy = staticPlannerStrategy{proposal: harness.PlanProposal{CreateBranches: []harness.BranchProposal{{ParentID: "root", Topic: "web", Goal: "web", Hypothesis: "web", Target: "target-1", SuccessCondition: "yes", FailureCondition: "no"}}}}
	spec.Executors[0].Runtime = &fakeRuntime{new: func(harness.RuntimeRequest) harness.AgentSession {
		return &fakeSession{events: make(chan harness.AgentEvent), prompt: func(context.Context, string) error {
			return errors.New("runtime unavailable")
		}}
	}}
	result, err = newTestRunner(t, store).Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	observed.collect(t, store, result)

	// Branch outcomes and attempt-level statuses.
	store = newMemoryStore()
	spec = integrationSpec(t, store)
	spec.Executors = append(spec.Executors, harness.AgentSpec{ID: "exec-2", Runtime: &fakeRuntime{}})
	spec.Policy.MaxActiveBranches = 2
	spec.PlannerStrategy = &branchStatePlanner{}
	spec.ExecutorStrategy = branchStateExecutor{}
	result, err = newTestRunner(t, store).Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	observed.collect(t, store, result)

	// Recovery marks an unfinished historical run interrupted.
	dbPath := filepath.Join(t.TempDir(), "reachability.db")
	sqliteStore, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer sqliteStore.Close()
	if err := sqliteStore.CreateRun(context.Background(), harness.RunRecord{ID: "interrupted-run", Status: harness.StatusRunning, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.NewRunner(harness.Config{Store: sqliteStore, Starter: noopStarter{}, WorkspaceRoot: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	record, err := sqliteStore.GetRun(context.Background(), "interrupted-run")
	if err != nil {
		t.Fatal(err)
	}
	observed.statuses[record.Status] = true

	statuses := []harness.Status{harness.StatusQueued, harness.StatusProvisioning, harness.StatusRunning, harness.StatusFinalizing, harness.StatusVerifying, harness.StatusSucceeded, harness.StatusFailed, harness.StatusCancelled, harness.StatusTimedOut, harness.StatusInterrupted, harness.StatusStagnated}
	for _, status := range statuses {
		if !observed.statuses[status] {
			t.Errorf("status %q has no production transition", status)
		}
	}
	branchStatuses := []harness.BranchStatus{harness.BranchOpen, harness.BranchActive, harness.BranchResolved, harness.BranchRefuted, harness.BranchBlocked, harness.BranchAbandoned}
	for _, status := range branchStatuses {
		if !observed.branches[status] {
			t.Errorf("branch status %q has no production transition", status)
		}
	}
	attemptStatuses := []harness.AttemptStatus{harness.AttemptRunning, harness.AttemptCompleted, harness.AttemptTruncated, harness.AttemptFailed, harness.AttemptCancelled}
	for _, status := range attemptStatuses {
		if !observed.attempts[status] {
			t.Errorf("attempt status %q has no production transition", status)
		}
	}
	outcomes := []harness.AttemptOutcome{harness.AttemptLocalSuccess, harness.AttemptRefuted, harness.AttemptBlocked, harness.AttemptInconclusive, harness.AttemptFailure}
	for _, outcome := range outcomes {
		if !observed.outcomes[outcome] {
			t.Errorf("attempt outcome %q has no production transition", outcome)
		}
	}
	trusts := []harness.KnowledgeTrust{harness.KnowledgeObserved, harness.KnowledgeCorroborated, harness.KnowledgeContested, harness.KnowledgeRefuted}
	for _, trust := range trusts {
		if !observed.trusts[trust] {
			t.Errorf("knowledge trust %q has no production transition", trust)
		}
	}
}

// branchStatePlanner creates one branch per declared outcome and abandons the
// last one through CloseBranches.
type branchStatePlanner struct{ closed bool }

func (p *branchStatePlanner) Prompt(harness.PlanRequest) (string, error) { return "plan", nil }
func (p *branchStatePlanner) Parse(req harness.PlanRequest, _ harness.TurnResult) (harness.PlanProposal, error) {
	topics := map[string]bool{}
	abandonID := ""
	for _, branch := range req.Branches {
		topics[branch.Topic] = true
		if branch.Topic == "abandon-me" && branch.Status == harness.BranchOpen {
			abandonID = branch.ID
		}
	}
	proposal := harness.PlanProposal{}
	if abandonID != "" && !p.closed {
		p.closed = true
		proposal.CloseBranches = []string{abandonID}
	}
	for _, topic := range []string{"refuted-me", "blocked-me", "truncated-me", "cancelled-me", "abandon-me"} {
		if topics[topic] {
			continue
		}
		proposal.CreateBranches = append(proposal.CreateBranches, harness.BranchProposal{ParentID: "root", Topic: topic, Goal: topic, Hypothesis: topic, Target: "target-1", SuccessCondition: "yes", FailureCondition: "no"})
	}
	return proposal, nil
}

type branchStateExecutor struct{}

func (branchStateExecutor) Prompt(harness.AttemptRequest) (string, error) { return "execute", nil }
func (branchStateExecutor) Parse(req harness.AttemptRequest, _ harness.TurnResult) (harness.AttemptReport, error) {
	if err := os.WriteFile(filepath.Join(req.WorkspaceDir, "proof.txt"), []byte("evidence"), 0o600); err != nil {
		return harness.AttemptReport{}, err
	}
	report := harness.AttemptReport{
		BranchID: req.Branch.ID, Status: harness.AttemptCompleted, Outcome: harness.AttemptInconclusive,
		Observations: []harness.ObservationProposal{{LocalID: "obs", Kind: "test.evidence", Summary: "observed", Target: "target-1", EvidenceFiles: []string{"proof.txt"}}},
	}
	switch req.Branch.Topic {
	case "refuted-me":
		report.Outcome = harness.AttemptRefuted
	case "blocked-me":
		report.Outcome = harness.AttemptBlocked
	case "truncated-me":
		report.Status = harness.AttemptTruncated
	case "cancelled-me":
		report.Status = harness.AttemptCancelled
	}
	return report, nil
}
