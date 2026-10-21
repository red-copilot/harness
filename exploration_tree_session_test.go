package harness

import (
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
