package engine

import (
	"errors"
	"testing"
	"time"
)

func testBoard(t *testing.T) *boardState {
	t.Helper()
	policy := DefaultRunPolicy()
	policy.Exploration.MaxOpenIntents = 2
	spec := RunSpec{
		Task:   Task{ID: "task-1", Objective: "test"},
		Target: TargetSpec{ID: "target-1", Isolation: IsolationCallerManaged},
		Policy: policy,
	}
	board := newBoardState("run-1", spec, TargetBinding{TargetID: "target-1", Isolation: IsolationCallerManaged})
	return board
}

func TestBoardStartsWithOriginAndGoal(t *testing.T) {
	board := testBoard(t)
	facts := board.snapshot().Facts
	if len(facts) != 2 || facts[0].Kind != FactOrigin || facts[1].Kind != FactGoal {
		t.Fatalf("initial facts = %+v", facts)
	}
}

func TestBoardRejectsThirdOpenIntent(t *testing.T) {
	board := testBoard(t)
	for i := 1; i <= 2; i++ {
		if _, err := board.createIntent(Intent{Kind: IntentExplore, FromFactIDs: []string{"origin"}, Description: "intent"}); err != nil {
			t.Fatalf("intent %d rejected: %v", i, err)
		}
	}
	if _, err := board.createIntent(Intent{Kind: IntentExplore, FromFactIDs: []string{"origin"}, Description: "third"}); err == nil {
		t.Fatal("third open intent accepted")
	}
	if got := board.openCount(); got != 2 {
		t.Fatalf("open intents = %d, want 2", got)
	}
}

func TestBoardRejectsIntentClaimOnOccupiedSlot(t *testing.T) {
	board := testBoard(t)
	first, err := board.createIntent(Intent{Kind: IntentExplore, FromFactIDs: []string{"origin"}, Description: "one"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := board.createIntent(Intent{Kind: IntentExplore, FromFactIDs: []string{"origin"}, Description: "two"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := board.claimIntent(first.ID, 1, "executor-1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := board.claimIntent(second.ID, 1, "executor-2", time.Now().UTC()); err == nil {
		t.Fatal("second claim on slot-1 accepted")
	}
	if _, err := board.claimIntent(second.ID, 2, "executor-2", time.Now().UTC()); err != nil {
		t.Fatalf("slot-2 claim rejected: %v", err)
	}
}

func TestBoardConcludeBindsFactAndReleasesSlot(t *testing.T) {
	board := testBoard(t)
	fact, err := board.addFact(Fact{Kind: FactFinding, Description: "found", ObservationIDs: []string{"obs-1"}})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := board.createIntent(Intent{Kind: IntentExplore, FromFactIDs: []string{"origin"}, Description: "one"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := board.claimIntent(intent.ID, 1, "executor-1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	concluded, err := board.concludeIntent(intent.ID, fact.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if concluded.State != IntentConcluded || concluded.ResultFactID != fact.ID {
		t.Fatalf("concluded intent = %+v", concluded)
	}
	if board.claimedCount() != 0 || board.openCount() != 0 {
		t.Fatalf("counts open=%d claimed=%d", board.openCount(), board.claimedCount())
	}
}

func TestBoardRetryReturnsIntentToOpen(t *testing.T) {
	board := testBoard(t)
	intent, err := board.createIntent(Intent{Kind: IntentExplore, FromFactIDs: []string{"origin"}, Description: "one"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := board.claimIntent(intent.ID, 1, "executor-1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	retried, err := board.returnIntentToOpen(intent.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if retried.State != IntentOpen || retried.Slot != 0 || retried.ExecutorID != "" {
		t.Fatalf("retried intent = %+v", retried)
	}
	if board.claimedCount() != 0 || board.openCount() != 1 {
		t.Fatalf("counts open=%d claimed=%d", board.openCount(), board.claimedCount())
	}
}

func TestReasonAdmissionCapsAtFreeSlots(t *testing.T) {
	board := testBoard(t)
	report := ReasonReport{FrontierVersion: board.frontierVersion(), Intents: []IntentProposal{
		{From: []string{"origin"}, Description: "one"},
		{From: []string{"origin"}, Description: "two"},
		{From: []string{"origin"}, Description: "three"},
	}}
	if _, err := admitReasonIntents(board, report, []int{1, 2}); err == nil {
		t.Fatal("three proposals admitted for two slots")
	}
	if len(board.snapshot().Intents) != 0 {
		t.Fatal("failed admission partially mutated the board")
	}
}

func TestReasonAdmissionRejectsStaleVersion(t *testing.T) {
	board := testBoard(t)
	version := board.frontierVersion()
	report := ReasonReport{FrontierVersion: version, Intents: []IntentProposal{{From: []string{"origin"}, Description: "one"}}}
	if _, err := admitReasonIntents(board, report, []int{1}); err != nil {
		t.Fatal(err)
	}
	stale := ReasonReport{FrontierVersion: version, Intents: []IntentProposal{{From: []string{"origin"}, Description: "stale"}}}
	if _, err := admitReasonIntents(board, stale, []int{2}); err == nil {
		t.Fatal("stale report accepted")
	}
	if len(board.snapshot().Intents) != 1 {
		t.Fatalf("intents = %d, want 1", len(board.snapshot().Intents))
	}
}

func TestReasonAdmissionNeverExceedsTwoClaimed(t *testing.T) {
	board := testBoard(t)
	for _, slot := range []int{1, 2} {
		intent, err := board.createIntent(Intent{Kind: IntentExplore, FromFactIDs: []string{"origin"}, Description: "work"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := board.claimIntent(intent.ID, slot, "executor", time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	report := ReasonReport{FrontierVersion: board.frontierVersion(), Intents: []IntentProposal{{From: []string{"origin"}, Description: "third"}}}
	if _, err := admitReasonIntents(board, report, []int{1}); err == nil {
		t.Fatal("admission accepted work with no capacity")
	}
}

func TestBoardRejectsFindingWithoutEvidence(t *testing.T) {
	board := testBoard(t)
	if _, err := board.addFact(Fact{Kind: FactFinding, Description: "unsupported"}); err == nil {
		t.Fatal("finding without evidence accepted")
	}
}

func TestReasonAdmissionReturnsFrontierCapacityError(t *testing.T) {
	board := testBoard(t)
	for i := 0; i < 2; i++ {
		if _, err := board.createIntent(Intent{Kind: IntentExplore, FromFactIDs: []string{"origin"}, Description: "work"}); err != nil {
			t.Fatal(err)
		}
	}
	report := ReasonReport{FrontierVersion: board.frontierVersion(), Intents: []IntentProposal{{From: []string{"origin"}, Description: "third"}}}
	if _, err := admitReasonIntents(board, report, []int{1}); !errors.Is(err, ErrFrontierCapacity) {
		t.Fatalf("err = %v, want ErrFrontierCapacity", err)
	}
}

func TestBlackboardRunSpecRequiresTwoExecutors(t *testing.T) {
	spec := RunSpec{
		Task: Task{ID: "task", Objective: "test"}, Target: TargetSpec{ID: "target", Isolation: IsolationCallerManaged},
		Executors: []AgentSpec{{ID: "one"}},
	}
	if err := validateBlackboardRunSpec(spec); err == nil {
		t.Fatal("one executor accepted by blackboard engine")
	}
}
