package core

import (
	"testing"
	"time"
)

func validState() State {
	return State{RunID: "run-1", Phase: PhaseQueued, HeadSeq: 1}
}

func at(seconds int64) time.Time {
	return time.Unix(seconds, 0).UTC()
}

func TestAdvanceHappyPath(t *testing.T) {
	state := validState()
	var err error
	state, err = Advance(state, Transition{Kind: TransitionStart, At: at(1)})
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != PhaseProvisioning || state.StartedAt == nil {
		t.Fatalf("state = %+v", state)
	}
	state, err = Advance(state, Transition{Kind: TransitionProvisioned, At: at(2), HeadSeq: 2})
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != PhaseRunning {
		t.Fatalf("phase = %q", state.Phase)
	}
	state, err = Advance(state, Transition{
		Kind: TransitionBoardRound, At: at(3), HeadSeq: 3, BoardRevision: 4,
		Usage: Usage{TotalTokens: 10}, NonGrowthRounds: 1, ReasonFailures: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.BoardRevision != 4 || state.NonGrowthRounds != 1 || state.Usage.TotalTokens != 10 {
		t.Fatalf("state = %+v", state)
	}
	state, err = Advance(state, Transition{Kind: TransitionGoalCandidate, At: at(4), HeadSeq: 5})
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != PhaseVerifying || !state.GoalCandidate {
		t.Fatalf("state = %+v", state)
	}
	state, err = Advance(state, Transition{
		Kind: TransitionFinalize, At: at(5), HeadSeq: 6, Outcome: OutcomeSucceeded,
		Verdict: &Verdict{Authority: VerdictAuthorityVerifier, Success: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != PhaseTerminal || state.Outcome != OutcomeSucceeded || state.FinishedAt == nil {
		t.Fatalf("state = %+v", state)
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAdvanceRejectsInvalidTransitions(t *testing.T) {
	queued := validState()
	running := State{RunID: "run-1", Phase: PhaseRunning, HeadSeq: 2, StartedAt: ptrTime(at(1)), Usage: Usage{TotalTokens: 10}}
	verifying := State{RunID: "run-1", Phase: PhaseVerifying, HeadSeq: 3, GoalCandidate: true, StartedAt: ptrTime(at(1))}
	terminal := State{RunID: "run-1", Phase: PhaseTerminal, Outcome: OutcomeFailed, HeadSeq: 3, FinishedAt: ptrTime(at(2))}
	stagnated := State{RunID: "run-1", Phase: PhaseRunning, HeadSeq: 2, Stagnated: true}

	cases := []struct {
		name       string
		state      State
		transition Transition
	}{
		{"start from running", running, Transition{Kind: TransitionStart, At: at(1)}},
		{"provision from queued", queued, Transition{Kind: TransitionProvisioned, At: at(1)}},
		{"board round from verifying", verifying, Transition{Kind: TransitionBoardRound, At: at(1)}},
		{"goal candidate from queued", queued, Transition{Kind: TransitionGoalCandidate, At: at(1)}},
		{"goal candidate from stagnated", stagnated, Transition{Kind: TransitionGoalCandidate, At: at(1)}},
		{"success from running", running, Transition{Kind: TransitionFinalize, At: at(1), Outcome: OutcomeSucceeded, Verdict: &Verdict{Success: true}}},
		{"success without verdict", verifying, Transition{Kind: TransitionFinalize, At: at(1), Outcome: OutcomeSucceeded}},
		{"success with failed verdict", verifying, Transition{Kind: TransitionFinalize, At: at(1), Outcome: OutcomeSucceeded, Verdict: &Verdict{Success: false}}},
		{"backend authority", verifying, Transition{Kind: TransitionFinalize, At: at(1), Outcome: OutcomeSucceeded, Verdict: &Verdict{Authority: VerdictAuthorityBackend, Success: true}}},
		{"terminal is immutable", terminal, Transition{Kind: TransitionFinalize, At: at(1), Outcome: OutcomeFailed}},
		{"outcome on non-finalize", running, Transition{Kind: TransitionBoardRound, At: at(1), Outcome: OutcomeFailed}},
		{"usage decreases", running, Transition{Kind: TransitionBoardRound, At: at(1), Usage: Usage{TotalTokens: 1}}},
		{"head does not advance", running, Transition{Kind: TransitionBoardRound, At: at(1), HeadSeq: 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Advance(tc.state, tc.transition); err == nil {
				t.Fatalf("transition %+v accepted", tc.transition)
			}
		})
	}
}

func TestAdvanceStagnationIsSticky(t *testing.T) {
	state := State{RunID: "run-1", Phase: PhaseRunning, HeadSeq: 2, Stagnated: true}
	next, err := Advance(state, Transition{Kind: TransitionBoardRound, At: at(1), NonGrowthRounds: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !next.Stagnated {
		t.Fatal("stagnation was cleared")
	}
	if _, err := Advance(next, Transition{Kind: TransitionGoalCandidate, At: at(2)}); err == nil {
		t.Fatal("stagnated run requested verification")
	}
}

func TestStateValidateRejectsIllegalStates(t *testing.T) {
	cases := []State{
		{},
		{RunID: "run-1", Phase: PhaseRunning, HeadSeq: 1, Outcome: OutcomeFailed},
		{RunID: "run-1", Phase: PhaseTerminal, Outcome: OutcomeFailed, HeadSeq: 1},
		{RunID: "run-1", Phase: PhaseQueued, HeadSeq: 1, Stagnated: true, GoalCandidate: true},
		{RunID: "run-1", Phase: PhaseRunning, HeadSeq: 1, Usage: Usage{TotalTokens: -1}},
	}
	for i, state := range cases {
		if err := state.Validate(); err == nil {
			t.Fatalf("case %d accepted: %+v", i, state)
		}
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func FuzzAdvance(f *testing.F) {
	f.Add("queued", "", "start", "", false)
	f.Add("provisioning", "", "provisioned", "", false)
	f.Add("running", "", "board_round", "", false)
	f.Add("running", "", "goal_candidate", "", false)
	f.Add("verifying", "succeeded", "finalize", "verifier", true)
	f.Add("running", "failed", "finalize", "", false)
	f.Fuzz(func(t *testing.T, phase, outcome, kind, authority string, verdictSuccess bool) {
		state := State{
			RunID: "run-1", Phase: Phase(phase), Outcome: Outcome(outcome), HeadSeq: 1,
			GoalCandidate: Phase(phase) == PhaseVerifying,
		}
		transition := Transition{
			Kind: TransitionKind(kind), At: at(1), Outcome: Outcome(outcome),
			Verdict: &Verdict{Authority: VerdictAuthority(authority), Success: verdictSuccess},
		}
		next, err := Advance(state, transition)
		if err != nil {
			return
		}
		if err := next.Validate(); err != nil {
			t.Fatalf("accepted transition produced invalid state %+v: %v", next, err)
		}
		if next.Phase == PhaseTerminal && next.Outcome == OutcomeSucceeded {
			if transition.Verdict == nil || !transition.Verdict.Success {
				t.Fatalf("success accepted without a successful verdict: %+v", next)
			}
		}
	})
}
