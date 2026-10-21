package harness

import (
	"strings"
	"testing"
	"time"
)

func TestKnowledgeDigestHonorsCountAndByteLimits(t *testing.T) {
	task := Task{ID: "task-1", Objective: "objective"}
	target := Target{ID: "target-1", Isolation: IsolationCallerManaged}
	now := time.Now().UTC()
	items := make([]Knowledge, 0, 20)
	for i := 0; i < 20; i++ {
		items = append(items, Knowledge{ID: string(rune('a' + i)), TaskID: task.ID, RunID: "run-1", Topic: "topic", Kind: "adapter.kind", Statement: strings.Repeat("界", 300), Scope: KnowledgeScopeTask, Trust: KnowledgeObserved, Reuse: ReuseStable, ValidFrom: now.Add(time.Duration(i) * time.Second)})
	}
	request, dropped := buildPlannerContext(task, target, nil, items, Completion{}, nil, DefaultExplorationPolicy())
	if len(request.Knowledge) == 0 || len(request.Knowledge) > 8 {
		t.Fatalf("knowledge count = %d", len(request.Knowledge))
	}
	total := 0
	for _, item := range request.Knowledge {
		if len(item.Statement) > 200 {
			t.Fatalf("statement bytes = %d", len(item.Statement))
		}
		total += len(item.Statement)
	}
	if total > 2048 {
		t.Fatalf("digest bytes = %d", total)
	}
	if dropped == 0 {
		t.Fatal("dropped count should be non-zero")
	}
}

func TestKnowledgeDigestOrdersDeterministically(t *testing.T) {
	now := time.Now().UTC()
	items := []Knowledge{
		{ID: "z-observed", TaskID: "task-1", Statement: "observed", Trust: KnowledgeObserved, ValidFrom: now},
		{ID: "b-corrob", TaskID: "task-1", Statement: "corrob-b", Trust: KnowledgeCorroborated, ValidFrom: now.Add(-time.Hour)},
		{ID: "a-corrob", TaskID: "task-1", Statement: "corrob-a", Trust: KnowledgeCorroborated, ValidFrom: now.Add(-time.Hour)},
		{ID: "c-contested", TaskID: "task-1", Statement: "contested", Trust: KnowledgeContested, ValidFrom: now},
	}
	req, _ := buildPlannerContext(Task{ID: "task-1", Objective: "x"}, Target{ID: "target-1", Isolation: IsolationCallerManaged}, nil, items, Completion{}, nil, DefaultExplorationPolicy())
	got := []string{}
	for _, item := range req.Knowledge {
		got = append(got, item.ID)
	}
	want := []string{"a-corrob", "b-corrob", "z-observed", "c-contested"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestKnowledgeDigestExcludesRefutedExpiredAndWrongScope(t *testing.T) {
	now := time.Now().UTC()
	expired := now.Add(-time.Minute)
	items := []Knowledge{
		{ID: "keep", TaskID: "task-1", Target: "target-1", Statement: "keep", Scope: KnowledgeScopeTarget, Trust: KnowledgeObserved, ValidFrom: now},
		{ID: "refuted", TaskID: "task-1", Target: "target-1", Statement: "refuted", Scope: KnowledgeScopeTarget, Trust: KnowledgeRefuted, ValidFrom: now},
		{ID: "expired", TaskID: "task-1", Target: "target-1", Statement: "expired", Scope: KnowledgeScopeTarget, Trust: KnowledgeObserved, ValidFrom: now, ExpiresAt: &expired},
		{ID: "wrong-target", TaskID: "task-1", Target: "other", Statement: "wrong", Scope: KnowledgeScopeTarget, Trust: KnowledgeObserved, ValidFrom: now},
	}
	req, _ := buildPlannerContext(Task{ID: "task-1", Objective: "x"}, Target{ID: "target-1", Isolation: IsolationCallerManaged}, nil, items, Completion{}, nil, DefaultExplorationPolicy())
	if len(req.Knowledge) != 1 || req.Knowledge[0].ID != "keep" {
		t.Fatalf("knowledge = %+v", req.Knowledge)
	}
}

func TestKnowledgeDigestDropsRefutedKnowledge(t *testing.T) {
	now := time.Now().UTC()
	refuted := Knowledge{ID: "k2", TaskID: "task-1", RunID: "run-1", Statement: "refuted", Trust: KnowledgeRefuted, ValidFrom: now}
	kept := Knowledge{ID: "k1", TaskID: "task-1", RunID: "run-1", Statement: "kept", Trust: KnowledgeCorroborated, ValidFrom: now}
	if _, dropped := buildPlannerContext(Task{ID: "task-1", Objective: "x"}, Target{ID: "target-1"}, nil, []Knowledge{refuted, kept}, Completion{}, nil, DefaultExplorationPolicy()); dropped != 1 {
		t.Fatalf("digest dropped = %d, want 1", dropped)
	}
}

func TestPlannerAndExecutorReceiveOnlyBoundedRecentReports(t *testing.T) {
	now := time.Now().UTC()
	branches := []Branch{{ID: "b1", Topic: "one", Status: BranchBlocked, CreatedAt: now}, {ID: "b2", Topic: "two", Status: BranchBlocked, CreatedAt: now}, {ID: "b3", Topic: "three", Status: BranchBlocked, CreatedAt: now}}
	attempts := []Attempt{{ID: "a1", BranchID: "b1", Status: AttemptFailed, Outcome: AttemptFailure}, {ID: "a2", BranchID: "b2", Status: AttemptFailed, Outcome: AttemptFailure}, {ID: "a3", BranchID: "b3", Status: AttemptFailed, Outcome: AttemptFailure}}
	planner, _ := buildPlannerContext(Task{ID: "task-1", Objective: "x"}, Target{ID: "target-1", Isolation: IsolationCallerManaged}, branches, nil, Completion{}, attempts, DefaultExplorationPolicy())
	if len(planner.LastReports) > 2 {
		t.Fatalf("planner reports = %d", len(planner.LastReports))
	}
	executor, _ := buildExecutorContext(Task{ID: "task-1", Objective: "x"}, Target{ID: "target-1", Isolation: IsolationCallerManaged}, branches[0], nil, AttemptBudget{MaxToolCalls: 20, MaxWallTime: time.Minute}, t.TempDir(), DefaultExplorationPolicy())
	if executor.Branch.ID != "b1" {
		t.Fatalf("executor branch = %q", executor.Branch.ID)
	}
}

// TestPlannerReportsAreTheLatestWindow pins the direction of the window: the
// planner must see the most recent attempts, not the oldest ones.
func TestPlannerReportsAreTheLatestWindow(t *testing.T) {
	now := time.Now().UTC()
	branches := []Branch{
		{ID: "b1", Topic: "one", Status: BranchBlocked, CreatedAt: now},
		{ID: "b2", Topic: "two", Status: BranchBlocked, CreatedAt: now},
		{ID: "b3", Topic: "three", Status: BranchBlocked, CreatedAt: now},
	}
	attempts := []Attempt{
		{ID: "a1", BranchID: "b1", Status: AttemptFailed, Outcome: AttemptFailure},
		{ID: "a2", BranchID: "b2", Status: AttemptFailed, Outcome: AttemptFailure},
		{ID: "a3", BranchID: "b3", Status: AttemptFailed, Outcome: AttemptFailure},
	}
	planner, _ := buildPlannerContext(Task{ID: "task-1", Objective: "x"}, Target{ID: "target-1", Isolation: IsolationCallerManaged}, branches, nil, Completion{}, attempts, DefaultExplorationPolicy())
	got := make([]string, 0, len(planner.LastReports))
	for _, report := range planner.LastReports {
		got = append(got, report.ID)
	}
	if strings.Join(got, ",") != "a2,a3" {
		t.Fatalf("reports = %v, want the latest window a2,a3", got)
	}
}
