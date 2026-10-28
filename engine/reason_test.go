package engine

import (
	"strings"
	"testing"
)

func TestReasonPromptIncludesOnlyBoundedGraph(t *testing.T) {
	board := testBoard(t)
	for i := 0; i < 100; i++ {
		if _, err := board.addFact(Fact{Kind: FactNegative, Description: "negative"}); err != nil {
			t.Fatal(err)
		}
	}
	req := buildReasonContext(board.snapshot(), board.target, nil, []int{1, 2}, nil, board.policy)
	if len(req.Facts) > maxReasonFacts {
		t.Fatalf("facts = %d, want <= %d", len(req.Facts), maxReasonFacts)
	}
	if req.Facts[0].Kind != FactOrigin || req.Facts[1].Kind != FactGoal {
		t.Fatalf("origin/goal missing from bounded context: %+v", req.Facts[:2])
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

func TestParseReasonReportStrictJSON(t *testing.T) {
	req := ReasonRequest{FrontierVersion: 7, FreeSlots: []int{1, 2}}
	turn := TurnResult{AssistantText: "```json\n{\"intents\":[{\"from\":[\"origin\"],\"description\":\"probe\"}]}\n```"}
	report, err := (defaultReasonStrategy{}).Parse(req, turn)
	if err != nil {
		t.Fatal(err)
	}
	if report.FrontierVersion != 7 || len(report.Intents) != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestReasonStrategyRejectsExtraIntents(t *testing.T) {
	req := ReasonRequest{FrontierVersion: 7, FreeSlots: []int{1, 2}}
	turn := TurnResult{AssistantText: `{"intents":[{"from":["origin"],"description":"a"},{"from":["origin"],"description":"b"},{"from":["origin"],"description":"c"}]}`}
	if _, err := (defaultReasonStrategy{}).Parse(req, turn); err == nil {
		t.Fatal("three intents accepted for two free slots")
	}
}

func TestReasonParserExtractsEmbeddedJSON(t *testing.T) {
	req := ReasonRequest{FrontierVersion: 7, FreeSlots: []int{1}}
	turn := TurnResult{AssistantText: "I will propose one direction.\n{\"intents\":[{\"from\":[\"origin\"],\"description\":\"probe\"}]}\nDone."}
	report, err := (defaultReasonStrategy{}).Parse(req, turn)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Intents) != 1 || report.Intents[0].Description != "probe" {
		t.Fatalf("report = %+v", report)
	}
}

func TestReasonContextHonorsPolicyByteAndItemBounds(t *testing.T) {
	board := testBoard(t)
	board.policy.Context.MaxBoardItems = 3
	board.policy.Context.MaxBoardBytes = 256
	board.policy.Context.MaxFactBytes = 40
	for i := 0; i < 10; i++ {
		if _, err := board.addFact(Fact{Kind: FactNegative, Description: "this-description-is-long-but-valid"}); err != nil {
			t.Fatal(err)
		}
	}
	board.policy.Context.MaxFactBytes = 12
	req := buildReasonContext(board.snapshot(), board.target, []Hint{{ID: "hint", Content: "this hint is longer than the configured limit"}}, []int{1, 2}, nil, board.policy)
	if len(req.Facts) > board.policy.Context.MaxBoardItems {
		t.Fatalf("facts = %d, want <= %d", len(req.Facts), board.policy.Context.MaxBoardItems)
	}
	for _, fact := range req.Facts {
		if len(fact.Description) > board.policy.Context.MaxFactBytes {
			t.Fatalf("fact description = %d bytes, want <= %d", len(fact.Description), board.policy.Context.MaxFactBytes)
		}
	}
}
