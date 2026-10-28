package engine

import (
	"strings"
	"testing"
)

func TestReasonPromptIncludesOnlyBoundedGraph(t *testing.T) {
	board := testBoard(t)
	for i := 0; i < 100; i++ {
		if _, err := board.addClaim(Claim{Kind: ClaimNegative, Description: "negative"}); err != nil {
			t.Fatal(err)
		}
	}
	req := buildReasonContext(board.snapshot(), board.target, nil, []int{1, 2}, nil, board.policy)
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
	if !strings.Contains(prompt, "Free slots: 1, 2") && !strings.Contains(prompt, "Free slots: 1,2") {
		t.Fatalf("prompt missing free slots:\n%s", prompt)
	}
	if !strings.Contains(prompt, board.task.Objective) {
		t.Fatalf("prompt missing task objective:\n%s", prompt)
	}
	if !strings.Contains(strings.ToLower(prompt), "verifier") {
		t.Fatalf("prompt does not preserve authority boundary:\n%s", prompt)
	}
}

func TestReasonPromptIncludesFreeSlotCount(t *testing.T) {
	board := testBoard(t)
	req := buildReasonContext(board.snapshot(), board.target, []Hint{{ID: "hint-1", Content: "focus web"}}, []int{2}, nil, board.policy)
	prompt, err := (defaultReasonStrategy{}).Prompt(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "At most 1") {
		t.Fatalf("prompt missing intent cap:\n%s", prompt)
	}
	if !strings.Contains(prompt, "focus web") {
		t.Fatalf("prompt missing hint:\n%s", prompt)
	}
}

func TestReasonPromptRequiresReportFile(t *testing.T) {
	board := testBoard(t)
	req := buildReasonContext(board.snapshot(), board.target, nil, []int{1}, nil, board.policy)
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
	req := ReasonRequest{FrontierVersion: 7, FreeSlots: []int{1}}
	turn := TurnResult{Report: []byte(`{"intents":[{"from":["origin"],"description":"probe"}]}`)}
	report, err := (defaultReasonStrategy{}).Parse(req, turn)
	if err != nil {
		t.Fatal(err)
	}
	if report.FrontierVersion != 7 || len(report.Intents) != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestReasonStrategyRejectsExtraIntents(t *testing.T) {
	req := ReasonRequest{FrontierVersion: 7, FreeSlots: []int{1}}
	turn := TurnResult{Report: []byte(`{"intents":[{"from":["origin"],"description":"a"},{"from":["origin"],"description":"b"},{"from":["origin"],"description":"c"}]}`)}
	if _, err := (defaultReasonStrategy{}).Parse(req, turn); err == nil {
		t.Fatal("three intents accepted for two free slots")
	}
}

func TestReasonParserRejectsNarrativeJSON(t *testing.T) {
	req := ReasonRequest{FrontierVersion: 7, FreeSlots: []int{1}}
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
		if _, err := board.addClaim(Claim{Kind: ClaimNegative, Description: "this-description-is-long-but-valid"}); err != nil {
			t.Fatal(err)
		}
	}
	board.policy.Context.MaxClaimBytes = 12
	req := buildReasonContext(board.snapshot(), board.target, []Hint{{ID: "hint", Content: "this hint is longer than the configured limit"}}, []int{1, 2}, nil, board.policy)
	if len(req.Claims) > board.policy.Context.MaxBoardItems {
		t.Fatalf("claims = %d, want <= %d", len(req.Claims), board.policy.Context.MaxBoardItems)
	}
	for _, claim := range req.Claims {
		if len(claim.Description) > board.policy.Context.MaxClaimBytes {
			t.Fatalf("claim description = %d bytes, want <= %d", len(claim.Description), board.policy.Context.MaxClaimBytes)
		}
	}
}
