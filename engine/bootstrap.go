package engine

import (
	"fmt"
	"strings"
)

type defaultBootstrapStrategy struct{}

func (defaultBootstrapStrategy) Prompt(req ExploreRequest) (string, error) {
	return bootstrapPrompt(req), nil
}

func (defaultBootstrapStrategy) Parse(req ExploreRequest, turn TurnResult) (AttemptReport, error) {
	var report AttemptReport
	if err := decodeReport(turn.Report, &report); err != nil {
		return AttemptReport{}, fmt.Errorf("parse bootstrap report: %w", err)
	}
	if report.IntentID == "" || strings.EqualFold(report.IntentID, "INTENT_ID") {
		report.IntentID = req.Intent.ID
	}
	if report.Phase == "" {
		report.Phase = IntentAttemptConcluded
	}
	if err := ValidateAttemptReport(report); err != nil {
		return AttemptReport{}, err
	}
	return report, nil
}

func bootstrapPrompt(req ExploreRequest) string {
	var out strings.Builder
	out.WriteString("You are the Bootstrap task for an authorized security challenge.\n")
	out.WriteString("Try to solve the whole task directly before graph exploration begins.\n")
	fmt.Fprintf(&out, "Intent: id=%s description=%s\n", req.Intent.ID, req.Intent.Description)
	fmt.Fprintf(&out, "Objective: %s\nTarget: id=%s addresses=%s\n", req.Task.Objective, req.Target.TargetID, strings.Join(req.Target.Addresses, ","))
	out.WriteString("Known claims:\n")
	for _, claim := range req.Claims {
		fmt.Fprintf(&out, "- id=%s kind=%s description=%s\n", claim.ID, claim.Kind, truncateUTF8(claim.Description, 1024))
	}
	out.WriteString("Hints:\n")
	for _, hint := range req.Hints {
		fmt.Fprintf(&out, "- %s\n", truncateUTF8(hint.Content, 512))
	}
	out.WriteString(`Return exactly one JSON object and nothing else.
Example finding: {"intent_id":"INTENT_ID","phase":"concluded","claim":{"kind":"finding","description":"confirmed result","observation_refs":["obs-1"]},"observations":[{"local_id":"obs-1","kind":"adapter.namespace","summary":"observed result","target":"TARGET_ID","evidence_files":["relative/file"]}]}
Example negative: {"intent_id":"INTENT_ID","phase":"concluded","claim":{"kind":"negative","description":"what was ruled out"}}
Do not create Intents, Branch nodes, Knowledge, or a Run verdict. Do not claim success.`)
	return out.String()
}

type bootstrapExploreStrategy struct{}

func (bootstrapExploreStrategy) ExecutePrompt(req ExploreRequest) (string, error) {
	return (defaultBootstrapStrategy{}).Prompt(req)
}
func (bootstrapExploreStrategy) ConcludePrompt(req ExploreRequest) (string, error) {
	return (defaultExploreStrategy{}).ConcludePrompt(req)
}
func (bootstrapExploreStrategy) Parse(req ExploreRequest, turn TurnResult) (AttemptReport, error) {
	return (defaultBootstrapStrategy{}).Parse(req, turn)
}
