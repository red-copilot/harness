package engine

import (
	"strings"
	"testing"
)

func TestReasonPromptIncludesOnlyBoundedGraph(t *testing.T) {
	board := testBoard(t)
	for i := 0; i < 100; i++ {
		if _, err := board.addClaim(Claim{Kind: ClaimNegative, Description: "negative", ObservationIDs: []string{"obs-1"}}); err != nil {
			t.Fatal(err)
		}
	}
	req := buildReasonContext(board.snapshot(), board.target, board.policy, Completion{})
	if len(req.Claims) > maxReasonClaims {
		t.Fatalf("claims = %d, want <= %d", len(req.Claims), maxReasonClaims)
	}
	if req.Claims[0].Kind != ClaimOrigin || req.Claims[1].Kind != ClaimGoal {
		t.Fatalf("origin/goal missing from bounded context: %+v", req.Claims[:2])
	}
	prompt, err := (defaultReasonStrategy{}).Prompt(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "At most one") {
		t.Fatalf("prompt missing intent cap:\n%s", prompt)
	}
	if !strings.Contains(prompt, board.task.Objective) {
		t.Fatalf("prompt missing task objective:\n%s", prompt)
	}
	if !strings.Contains(strings.ToLower(prompt), "verifier") {
		t.Fatalf("prompt does not preserve authority boundary:\n%s", prompt)
	}
}

func TestReasonPromptIncludesProgress(t *testing.T) {
	board := testBoard(t)
	req := buildReasonContext(board.snapshot(), board.target, board.policy, Completion{Progress: 2, Total: 5})
	prompt, err := (defaultReasonStrategy{}).Prompt(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Authoritative progress: 2/5") {
		t.Fatalf("prompt missing progress:\n%s", prompt)
	}
}

func TestReasonPromptRequiresReportFile(t *testing.T) {
	board := testBoard(t)
	req := buildReasonContext(board.snapshot(), board.target, board.policy, Completion{})
	prompt, err := (defaultReasonStrategy{}).Prompt(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/workspace/report.json", "assistant text is ignored", "30 seconds"} {
		if !strings.Contains(strings.ToLower(prompt), strings.ToLower(want)) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestParseReasonReportStrictJSON(t *testing.T) {
	req := ReasonRequest{BoardRevision: 7, CanCreateIntent: true}
	turn := TurnResult{Report: []byte(`{"intents":[{"from":["origin"],"description":"probe"}]}`)}
	report, err := (defaultReasonStrategy{}).Parse(req, turn)
	if err != nil {
		t.Fatal(err)
	}
	if report.BoardRevision != 7 || len(report.Intents) != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestReasonStrategyRejectsExtraIntents(t *testing.T) {
	req := ReasonRequest{BoardRevision: 7, CanCreateIntent: true}
	turn := TurnResult{Report: []byte(`{"intents":[{"from":["origin"],"description":"a"},{"from":["origin"],"description":"b"}]}`)}
	if _, err := (defaultReasonStrategy{}).Parse(req, turn); err == nil {
		t.Fatal("two intents accepted for one slot")
	}
}

func TestReasonParserRejectsNarrativeJSON(t *testing.T) {
	req := ReasonRequest{BoardRevision: 7, CanCreateIntent: true}
	turn := TurnResult{Report: []byte("I will propose one direction.\n{\"intents\":[{\"from\":[\"origin\"],\"description\":\"probe\"}]}\nDone.")}
	if _, err := (defaultReasonStrategy{}).Parse(req, turn); err == nil {
		t.Fatal("narrative report accepted")
	}
}

func TestReasonContextHonorsPolicyByteAndItemBounds(t *testing.T) {
	board := testBoard(t)
	board.policy.Context.MaxBoardItems = 3
	board.policy.Context.MaxBoardBytes = 256
	board.policy.Context.MaxClaimBytes = 40
	for i := 0; i < 10; i++ {
		if _, err := board.addClaim(Claim{Kind: ClaimNegative, Description: "this-description-is-long-but-valid", ObservationIDs: []string{"obs-1"}}); err != nil {
			t.Fatal(err)
		}
	}
	board.policy.Context.MaxClaimBytes = 12
	req := buildReasonContext(board.snapshot(), board.target, board.policy, Completion{})
	if len(req.Claims) > board.policy.Context.MaxBoardItems {
		t.Fatalf("claims = %d, want <= %d", len(req.Claims), board.policy.Context.MaxBoardItems)
	}
	for _, claim := range req.Claims {
		if len(claim.Description) > board.policy.Context.MaxClaimBytes {
			t.Fatalf("claim description = %d bytes, want <= %d", len(claim.Description), board.policy.Context.MaxClaimBytes)
		}
	}
}
