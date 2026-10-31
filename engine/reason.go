package engine

import (
	"fmt"
	"strings"
)

const maxReasonClaims = 64

type defaultReasonStrategy struct{}

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

func buildReasonContext(snapshot boardSnapshot, target TargetBinding, policy RunPolicy, progress Completion) ReasonRequest {
	policy = NormalizeRunPolicy(policy)
	claims := boundedClaimsWithPolicy(snapshot.Claims, policy)
	intents := make([]Intent, 0, 1)
	for _, intent := range snapshot.Intents {
		if intent.State == IntentOpen || intent.State == IntentClaimed {
			intents = append(intents, intent)
		}
	}
	if len(intents) > 1 {
		intents = intents[len(intents)-1:]
	}
	return ReasonRequest{
		Task:            snapshot.Task,
		Target:          target,
		Claims:          claims,
		Intents:         intents,
		CanCreateIntent: true,
		BoardRevision:   snapshot.BoardRevision,
		Progress:        progress,
		Policy:          policy,
	}
}

func boundedClaims(claims []Claim, limit int) []Claim {
	if limit <= 0 || len(claims) <= limit {
		return cloneClaims(claims)
	}
	out := make([]Claim, 0, limit)
	for _, claim := range claims {
		if claim.Kind == ClaimOrigin || claim.Kind == ClaimGoal {
			out = append(out, claim)
		}
	}
	for _, claim := range claims {
		if len(out) >= limit {
			break
		}
		if claim.Kind == ClaimOrigin || claim.Kind == ClaimGoal {
			continue
		}
		out = append(out, claim)
	}
	return cloneClaims(out)
}

func boundedClaimsWithPolicy(claims []Claim, policy RunPolicy) []Claim {
	limit := policy.Context.MaxBoardItems
	if limit <= 0 || limit > maxReasonClaims {
		limit = maxReasonClaims
	}
	base := boundedClaims(claims, limit)
	out := make([]Claim, 0, len(base))
	var used int64
	for _, claim := range base {
		claim.Description = truncateUTF8(claim.Description, policy.Context.MaxClaimBytes)
		cost := int64(len(claim.Description) + len(claim.ID) + 32)
		if len(out) >= 2 && policy.Context.MaxBoardBytes > 0 && used+cost > policy.Context.MaxBoardBytes {
			continue
		}
		used += cost
		out = append(out, claim)
	}
	return out
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
