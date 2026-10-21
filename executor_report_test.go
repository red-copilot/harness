package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExecutorReportHonorsMaxReportBytes pins the policy-enforced cap: the
// report parser previously used a fixed 1 MiB limit, so a policy that lowers
// MaxReportBytes had no effect.
func TestExecutorReportHonorsMaxReportBytes(t *testing.T) {
	dir := t.TempDir()
	report := `{"branch_id":"b1","status":"completed","outcome":"blocked"}`
	report = strings.Replace(report, "}", `,"padding":"`+strings.Repeat("x", 200)+`"}`, 1)
	if err := os.WriteFile(filepath.Join(dir, ReportFileName), []byte(report), 0o600); err != nil {
		t.Fatal(err)
	}
	req := AttemptRequest{Branch: Branch{ID: "b1"}, WorkspaceDir: dir, MaxReportBytes: 128}
	if _, err := (defaultExecutorStrategy{}).Parse(req, TurnResult{}); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want report too large", err)
	}
	if _, err := (defaultExecutorStrategy{}).Parse(AttemptRequest{Branch: Branch{ID: "b1"}, WorkspaceDir: dir}, TurnResult{}); err != nil {
		t.Fatalf("default cap rejected a small report: %v", err)
	}
}
