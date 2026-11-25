package engine

import (
	"strings"
	"testing"

	"github.com/red-copilot/harness/core"
)

func reasonTestRequest() ReasonRequest {
	return ReasonRequest{
		Task:   core.Task{ID: "task-1", Objective: "bounded objective"},
		Target: core.TargetBinding{TargetID: "target-1"},
		Claims: []core.Claim{
			{ID: "origin", Kind: core.ClaimOrigin, Description: "objective"},
			{ID: "goal", Kind: core.ClaimGoal, Description: "goal"},
		},
		CanCreateIntent: true,
		BoardRevision:   3,
	}
}

func TestReasonPromptPreservesSingleSlotAndAuthority(t *testing.T) {
	req := reasonTestRequest()
	prompt, err := (defaultReasonStrategy{}).Prompt(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "At most one") {
		t.Fatalf("prompt missing intent cap:\n%s", prompt)
	}
	if !strings.Contains(prompt, "bounded objective") {
		t.Fatalf("prompt missing task objective:\n%s", prompt)
	}
	if !strings.Contains(strings.ToLower(prompt), "verifier") {
		t.Fatalf("prompt does not preserve authority boundary:\n%s", prompt)
	}
}

func TestReasonPromptIncludesProgress(t *testing.T) {
	req := reasonTestRequest()
	req.Progress = Completion{Progress: 2, Total: 5}
	prompt, err := (defaultReasonStrategy{}).Prompt(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Authoritative progress: 2/5") {
		t.Fatalf("prompt missing progress:\n%s", prompt)
	}
}

func TestReasonPromptRequiresReportFile(t *testing.T) {
	prompt, err := (defaultReasonStrategy{}).Prompt(reasonTestRequest())
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
