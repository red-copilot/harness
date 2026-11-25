package engine

import (
	"fmt"
	"strings"
)

type defaultReasonStrategy struct{}

// DefaultReasonStrategy returns the protocol-compliant Reason strategy.
func DefaultReasonStrategy() ReasonStrategy { return defaultReasonStrategy{} }

func (defaultReasonStrategy) Prompt(req ReasonRequest) (string, error) {
	return reasonPrompt(req), nil
}

func (defaultReasonStrategy) Parse(req ReasonRequest, turn TurnResult) (ReasonReport, error) {
	var report ReasonReport
	if err := decodeReport(turn.Report, &report); err != nil {
		return ReasonReport{}, fmt.Errorf("parse reason report: %w", err)
	}
	if report.BoardRevision == 0 {
		report.BoardRevision = req.BoardRevision
	}
	if err := ValidateReasonReport(report, req.CanCreateIntent); err != nil {
		return ReasonReport{}, err
	}
	return report, nil
}

func reasonPrompt(req ReasonRequest) string {
	var out strings.Builder
	out.WriteString("You are the Reason task for an authorized security run.\n")
	out.WriteString("Read the Claim/Intent graph and choose the next exploration Intent.\n")
	fmt.Fprintf(&out, "Objective: %s\nTarget: id=%s addresses=%s\n", req.Task.Objective, req.Target.TargetID, strings.Join(req.Target.Addresses, ","))
	fmt.Fprintf(&out, "Board revision: %d\n", req.BoardRevision)
	if req.CanCreateIntent {
		out.WriteString("One Executor slot is free. At most one new Intent may be created.\n")
	} else {
		out.WriteString("No Executor slot is free. Do not create an Intent.\n")
	}
	if req.Progress.Total > 0 {
		fmt.Fprintf(&out, "Authoritative progress: %d/%d\n", req.Progress.Progress, req.Progress.Total)
	}
	out.WriteString("Claims:\n")
	for _, claim := range req.Claims {
		fmt.Fprintf(&out, "- id=%s kind=%s description=%s\n", claim.ID, claim.Kind, truncateUTF8(claim.Description, 1024))
	}
	out.WriteString("Open or claimed Intents:\n")
	for _, intent := range req.Intents {
		fmt.Fprintf(&out, "- id=%s state=%s executor=%s description=%s\n", intent.ID, intent.State, intent.ExecutorID, truncateUTF8(intent.Description, 512))
	}
	out.WriteString(reportProtocolInstruction)
	out.WriteString(`Return exactly one JSON object and nothing else. Do not mark the Run successful; only the Verifier or backend completion may do that. A goal_candidate only requests verification.
Example: {"board_revision":1,"intents":[{"from":["origin"],"description":"one bounded exploration direction"}]}
If the claims appear sufficient, return {"board_revision":1,"goal_candidate":{"from":["claim-id"],"description":"why the evidence may satisfy the goal"}}.
Do not create more than one Intent, do not create child nodes, and do not repeat an existing open Intent.`)
	return out.String()
}
