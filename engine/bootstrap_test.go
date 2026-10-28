package engine

import (
	"strings"
	"testing"
)

func TestBootstrapPromptStartsWithOriginGoalAndHints(t *testing.T) {
	board := testBoard(t)
	req := ExploreRequest{
		Task:         board.task,
		Target:       board.target,
		Intent:       Intent{ID: "bootstrap", RunID: board.runID, Kind: IntentBootstrap, FromFactIDs: []string{"origin", "goal"}, Description: "solve directly"},
		Facts:        board.snapshot().Facts,
		Hints:        []Hint{{ID: "hint-1", Content: "authorized hint"}},
		WorkspaceDir: "/workspace",
	}
	prompt, err := (defaultBootstrapStrategy{}).Prompt(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{req.Task.Objective, "authorized hint", "bootstrap"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBootstrapReportCannotCreateChildIntentsOrGoal(t *testing.T) {
	req := ExploreRequest{Intent: Intent{ID: "bootstrap"}}
	for _, body := range []string{
		`{"intent_id":"bootstrap","phase":"concluded","fact":{"kind":"goal","description":"done"}}`,
		`{"intent_id":"bootstrap","phase":"concluded","fact":{"kind":"finding","description":"done"}}`,
	} {
		if _, err := (defaultBootstrapStrategy{}).Parse(req, TurnResult{AssistantText: body}); err == nil {
			t.Fatalf("invalid bootstrap report accepted: %s", body)
		}
	}
}
