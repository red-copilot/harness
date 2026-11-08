package engine

import (
	"errors"
	"testing"
	"time"
)

func testBoard(t *testing.T) *boardState {
	t.Helper()
	policy := DefaultRunPolicy()
	spec := RunSpec{
		Task:   Task{ID: "task-1", Objective: "test"},
		Target: TargetSpec{ID: "target-1", Isolation: IsolationCallerManaged},
		Policy: policy,
	}
	return newBoardState("run-1", spec, TargetBinding{TargetID: "target-1", Isolation: IsolationCallerManaged})
}

func TestBoardStartsWithOriginAndGoal(t *testing.T) {
	board := testBoard(t)
	claims := board.snapshot().Claims
	if len(claims) != 2 || claims[0].Kind != ClaimOrigin || claims[1].Kind != ClaimGoal {
		t.Fatalf("initial claims = %+v", claims)
	}
}

func TestBoardRejectsSecondOpenIntent(t *testing.T) {
	board := testBoard(t)
	if _, err := board.createIntent(Intent{FromClaimIDs: []string{"origin"}, Description: "first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := board.createIntent(Intent{FromClaimIDs: []string{"origin"}, Description: "second"}); err == nil {
		t.Fatal("second open intent accepted")
	}
	if got := board.openCount(); got != 1 {
		t.Fatalf("open intents = %d, want 1", got)
	}
}

func TestBoardRejectsSecondClaim(t *testing.T) {
	board := testBoard(t)
	intent, err := board.createIntent(Intent{FromClaimIDs: []string{"origin"}, Description: "one"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := board.createIntent(Intent{FromClaimIDs: []string{"origin"}, Description: "two"})
	if err == nil {
		t.Fatalf("second open intent accepted: %+v", other)
	}
	if _, err := board.claimIntent(intent.ID, "executor", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := board.claimIntent(intent.ID, "executor", time.Now().UTC()); err == nil {
		t.Fatal("second claim accepted")
	}
}

func TestBoardConcludeBindsClaimAndReleasesSlot(t *testing.T) {
	board := testBoard(t)
	claim, err := board.addClaim(Claim{Kind: ClaimFinding, Description: "found", ObservationIDs: []string{"obs-1"}})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := board.createIntent(Intent{FromClaimIDs: []string{"origin"}, Description: "one"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := board.claimIntent(intent.ID, "executor-1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	concluded, err := board.concludeIntent(intent.ID, claim.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if concluded.State != IntentConcluded || concluded.ResultClaimID != claim.ID {
		t.Fatalf("concluded intent = %+v", concluded)
	}
	if board.claimedCount() != 0 || board.openCount() != 0 {
		t.Fatalf("counts open=%d claimed=%d", board.openCount(), board.claimedCount())
	}
}

func TestBoardRetryReturnsIntentToOpen(t *testing.T) {
	board := testBoard(t)
	intent, err := board.createIntent(Intent{FromClaimIDs: []string{"origin"}, Description: "one"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := board.claimIntent(intent.ID, "executor-1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	retried, err := board.returnIntentToOpen(intent.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if retried.State != IntentOpen || retried.ExecutorID != "" {
		t.Fatalf("retried intent = %+v", retried)
	}
	if board.claimedCount() != 0 || board.openCount() != 1 {
		t.Fatalf("counts open=%d claimed=%d", board.openCount(), board.claimedCount())
	}
}

func TestReasonAdmissionCapsAtSingleSlot(t *testing.T) {
	board := testBoard(t)
	report := ReasonReport{BoardRevision: board.boardRevision(), Intents: []IntentProposal{
		{From: []string{"origin"}, Description: "one"},
		{From: []string{"origin"}, Description: "two"},
	}}
	if _, err := admitReasonIntents(board, report, true); err == nil {
		t.Fatal("two proposals admitted for one slot")
	}
	if len(board.snapshot().Intents) != 0 {
		t.Fatal("failed admission partially mutated the board")
	}
	report.Intents = report.Intents[:1]
	if _, err := admitReasonIntents(board, report, true); err != nil {
		t.Fatal(err)
	}
}

func TestReasonAdmissionRejectsStaleRevision(t *testing.T) {
	board := testBoard(t)
	revision := board.boardRevision()
	report := ReasonReport{BoardRevision: revision, Intents: []IntentProposal{{From: []string{"origin"}, Description: "one"}}}
	if _, err := admitReasonIntents(board, report, true); err != nil {
		t.Fatal(err)
	}
	stale := ReasonReport{BoardRevision: revision, Intents: []IntentProposal{{From: []string{"origin"}, Description: "stale"}}}
	if _, err := admitReasonIntents(board, stale, true); err == nil {
		t.Fatal("stale report accepted")
	}
	if len(board.snapshot().Intents) != 1 {
		t.Fatalf("intents = %d, want 1", len(board.snapshot().Intents))
	}
}

func TestReasonAdmissionRejectsWhenExecutorClaimed(t *testing.T) {
	board := testBoard(t)
	intent, err := board.createIntent(Intent{FromClaimIDs: []string{"origin"}, Description: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := board.claimIntent(intent.ID, "executor", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	report := ReasonReport{BoardRevision: board.boardRevision(), Intents: []IntentProposal{{From: []string{"origin"}, Description: "next"}}}
	if _, err := admitReasonIntents(board, report, false); err == nil {
		t.Fatal("admission accepted work with no capacity")
	}
}

func TestBoardRejectsFindingWithoutEvidence(t *testing.T) {
	board := testBoard(t)
	if _, err := board.addClaim(Claim{Kind: ClaimFinding, Description: "unsupported"}); err == nil {
		t.Fatal("finding without evidence accepted")
	}
}

func TestReasonAdmissionReturnsIntentCapacityError(t *testing.T) {
	board := testBoard(t)
	if _, err := board.createIntent(Intent{FromClaimIDs: []string{"origin"}, Description: "work"}); err != nil {
		t.Fatal(err)
	}
	report := ReasonReport{BoardRevision: board.boardRevision(), Intents: []IntentProposal{{From: []string{"origin"}, Description: "third"}}}
	if _, err := admitReasonIntents(board, report, false); !errors.Is(err, ErrIntentCapacity) {
		t.Fatalf("err = %v, want ErrIntentCapacity", err)
	}
}

func TestBlackboardRunSpecRequiresExecutorRuntime(t *testing.T) {
	spec := RunSpec{
		Task: Task{ID: "task", Objective: "test"}, Target: TargetSpec{ID: "target", Isolation: IsolationCallerManaged},
		Executor: AgentSpec{ID: "executor"},
	}
	if err := validateBlackboardRunSpec(spec); err == nil {
		t.Fatal("executor without runtime accepted by blackboard engine")
	}
}
