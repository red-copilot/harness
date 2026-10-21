package exploration

import (
	"context"
	"github.com/red-copilot/harness"
	"strings"
	"testing"
)

func TestParseAttemptReportRejectsMismatchedBranch(t *testing.T) {
	_, err := ParseAttemptReport([]byte(`{"branch_id":"other","status":"completed","outcome":"local_success"}`), "branch-1")
	if err == nil || !strings.Contains(err.Error(), "branch_id") {
		t.Fatalf("ParseAttemptReport error = %v, want branch_id error", err)
	}
}

func TestParseAttemptReportRejectsUnknownOutcome(t *testing.T) {
	_, err := ParseAttemptReport([]byte(`{"branch_id":"branch-1","status":"completed","outcome":"maybe"}`), "branch-1")
	if err == nil || !strings.Contains(err.Error(), "outcome") {
		t.Fatalf("ParseAttemptReport error = %v, want outcome error", err)
	}
}

func TestParseAttemptReportAcceptsStructuredObservationsAndKnowledge(t *testing.T) {
	report, err := ParseAttemptReport([]byte(`{
		"branch_id":"branch-1",
		"status":"completed",
		"outcome":"local_success",
		"observations":[{"id":"obs-1","kind":"http","action":"GET /","target":"b-02","summary":"HTTP 200","evidence_refs":["raw-1"]}],
		"knowledge":[{"topic":"web.oa","kind":"anchor","statement":"OA path exists","scope":"target","target":"b-02","evidence_refs":["obs-1"],"reuse":"stable"}]
	}`), "branch-1")
	if err != nil {
		t.Fatalf("ParseAttemptReport: %v", err)
	}
	if len(report.Observations) != 1 || len(report.Knowledge) != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestParseBranchPlanOutputStripsCodeFence(t *testing.T) {
	output, err := ParseBranchPlanOutput([]byte("```json\n{\"create_branches\":[{\"topic\":\"web.oa\",\"target\":\"b-02\"}]}\n```"))
	if err != nil {
		t.Fatalf("ParseBranchPlanOutput: %v", err)
	}
	if len(output.CreateBranches) != 1 || output.CreateBranches[0].Topic != "web.oa" {
		t.Fatalf("output = %+v", output)
	}
}

type plannerScopeExecutor struct{}

func (e *plannerScopeExecutor) ID() string                  { return "executor-1" }
func (e *plannerScopeExecutor) Reset(context.Context) error { return nil }
func (e *plannerScopeExecutor) Attempt(_ context.Context, branch Branch, _ BranchBrief) (AttemptReport, error) {
	if branch.Depth == 1 {
		return AttemptReport{
			BranchID: branch.ID, Status: AttemptCompleted, Outcome: AttemptLocalSuccess,
			Observations: []Observation{{ID: "obs-1", Kind: "http", Summary: "parent evidence", EvidenceRefs: []string{"raw-1"}}},
		}, nil
	}
	return AttemptReport{BranchID: branch.ID, Status: AttemptCompleted, Outcome: AttemptRefuted}, nil
}

type nonRootPlanner struct {
	calls int
}

func (p *nonRootPlanner) Plan(_ context.Context, request BranchPlanRequest) (BranchPlanOutput, error) {
	p.calls++
	if p.calls == 1 {
		return BranchPlanOutput{CreateBranches: []BranchProposal{{
			ParentID: "root", Topic: "web.oa", Goal: "find OA", Hypothesis: "OA exists",
			Target: "b-02", SuccessCondition: "OA found", FailureCondition: "no OA",
		}}}, nil
	}
	if p.calls == 2 {
		for _, branch := range request.Branches {
			if branch.Depth == 1 {
				return BranchPlanOutput{CreateBranches: []BranchProposal{{
					ParentID: branch.ID, Topic: "web.oa.deep", Goal: "deep OA", Hypothesis: "deep OA exists",
					Target: "b-02", SuccessCondition: "deep page", FailureCondition: "no deep page",
				}}}, nil
			}
		}
	}
	return BranchPlanOutput{}, nil
}

type rootSeedPlanner struct {
	calls     int
	proposals []BranchProposal
}

func (p *rootSeedPlanner) Plan(context.Context, BranchPlanRequest) (BranchPlanOutput, error) {
	p.calls++
	if p.calls == 1 {
		return BranchPlanOutput{CreateBranches: append([]BranchProposal(nil), p.proposals...)}, nil
	}
	return BranchPlanOutput{}, nil
}

func sessionBranchRejection(events []BranchEvent, reason string) (map[string]any, bool) {
	for _, event := range events {
		if event.Type != "exploration.branch.rejected" {
			continue
		}
		data, ok := event.Data.(map[string]any)
		if !ok || data["reason"] != reason {
			continue
		}
		return data, true
	}
	return nil, false
}

func TestPlannerCannotExpandNonRoot(t *testing.T) {
	target := harness.Target{ID: "b-02", Addresses: []string{"10.0.0.1:80"}}
	planner := &nonRootPlanner{}
	var events []BranchEvent
	engine := NewBranchEngine(BranchEngineConfig{
		RunID: "run-1", Task: harness.Task{ID: "task-1", Objective: "find flag"}, Targets: []harness.Target{target},
		Policy: DefaultExplorationPolicy(),
		AcquireExecutors: func(context.Context, int) ([]BranchExecutor, error) {
			return []BranchExecutor{&plannerScopeExecutor{}}, nil
		},
	}, planner, func(event BranchEvent) error { events = append(events, event); return nil })
	if err := engine.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var parent Branch
	for _, branch := range engine.Branches() {
		if branch.Depth == 1 {
			parent = branch
			break
		}
	}
	if parent.ID == "" {
		t.Fatalf("depth-1 branch missing: %+v", engine.Branches())
	}
	for _, branch := range engine.Branches() {
		if branch.ParentID == parent.ID {
			t.Fatalf("planner created non-root child: %+v", branch)
		}
	}
	data, ok := sessionBranchRejection(events, "planner_cannot_expand")
	if !ok {
		t.Fatalf("missing planner_cannot_expand rejection: %+v", events)
	}
	if data["parent_id"] != parent.ID {
		t.Fatalf("rejection parent_id = %v, want %q", data["parent_id"], parent.ID)
	}
}

func TestPlannerRootSeedingRespectsMaxNodes(t *testing.T) {
	target := harness.Target{ID: "b-02", Addresses: []string{"10.0.0.1:80"}}
	planner := &rootSeedPlanner{proposals: []BranchProposal{
		{ParentID: "root", Topic: "web.oa", Goal: "find OA", Hypothesis: "OA exists", Target: "b-02", SuccessCondition: "OA found", FailureCondition: "no OA"},
		{ParentID: "root", Topic: "web.alt", Goal: "find alternate", Hypothesis: "alternate exists", Target: "b-02", SuccessCondition: "alternate found", FailureCondition: "no alternate"},
		{ParentID: "root", Topic: "web.admin", Goal: "find admin", Hypothesis: "admin exists", Target: "b-02", SuccessCondition: "admin found", FailureCondition: "no admin"},
		{ParentID: "root", Topic: "web.api", Goal: "find API", Hypothesis: "API exists", Target: "b-02", SuccessCondition: "API found", FailureCondition: "no API"},
	}}
	policy := DefaultExplorationPolicy()
	policy.MaxNodes = 3
	policy.MaxChildrenPerBranch = 1
	var events []BranchEvent
	engine := NewBranchEngine(BranchEngineConfig{
		RunID: "run-1", Task: harness.Task{ID: "task-1", Objective: "find flag"}, Targets: []harness.Target{target},
		Policy: policy,
		AcquireExecutors: func(context.Context, int) ([]BranchExecutor, error) {
			return []BranchExecutor{&plannerScopeExecutor{}}, nil
		},
	}, planner, func(event BranchEvent) error { events = append(events, event); return nil })
	if err := engine.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	branches := engine.Branches()
	if len(branches) != 3 {
		t.Fatalf("branches = %d, want root + 2 within max nodes: %+v", len(branches), branches)
	}
	var rejected bool
	for _, event := range events {
		if event.Type != "exploration.branch.rejected" {
			continue
		}
		data, ok := event.Data.(map[string]any)
		if !ok {
			continue
		}
		reason, _ := data["reason"].(string)
		if strings.Contains(reason, "max nodes") {
			rejected = true
			break
		}
	}
	if !rejected {
		t.Fatalf("missing max nodes rejection: %+v", events)
	}
}
