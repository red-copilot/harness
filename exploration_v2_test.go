package harness

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDefaultExplorationPolicyUsesBinaryTreeDefaults(t *testing.T) {
	p := DefaultExplorationPolicy()
	if p.MaxDepth != 4 || p.MaxNodes != 15 || p.MaxChildrenPerBranch != 2 || p.MaxActiveBranches != 2 {
		t.Fatalf("tree defaults = depth %d nodes %d children %d active %d, want 4/15/2/2", p.MaxDepth, p.MaxNodes, p.MaxChildrenPerBranch, p.MaxActiveBranches)
	}
	if p.MaxAttemptsPerBranch != 3 || p.MaxToolCallsPerAttempt != 20 || p.MaxWallTimePerAttempt != 10*time.Minute {
		t.Fatalf("attempt defaults = attempts %d tools %d wall %s", p.MaxAttemptsPerBranch, p.MaxToolCallsPerAttempt, p.MaxWallTimePerAttempt)
	}
	if p.StagnationRounds != 3 {
		t.Fatalf("stagnation rounds = %d, want 3", p.StagnationRounds)
	}
	normalized := NormalizeExplorationPolicy(ExplorationPolicy{MaxDepth: 4})
	if normalized.MaxChildrenPerBranch != 2 {
		t.Fatalf("normalized max children = %d, want 2", normalized.MaxChildrenPerBranch)
	}
}

func TestValidateBranchProposalRejectsUnauthorizedTarget(t *testing.T) {
	root := Branch{ID: "root", TaskID: "task-1", Depth: 0, Topic: "root", Scope: "b-02", Status: BranchOpen}
	_, err := ValidateBranchProposal(BranchProposal{
		ParentID: root.ID, Topic: "web.oa", Goal: "find OA", Hypothesis: "OA is exposed",
		Target: "other-target", SuccessCondition: "OA login page", FailureCondition: "no OA page",
	}, &root, []Target{{ID: "b-02", Addresses: []string{"10.0.0.1:80"}}}, nil, DefaultExplorationPolicy())
	if err == nil || !strings.Contains(err.Error(), "authorized") {
		t.Fatalf("ValidateBranchProposal error = %v, want authorized target error", err)
	}
}

func TestValidateBranchProposalRejectsDuplicateTopicScope(t *testing.T) {
	root := Branch{ID: "root", TaskID: "task-1", Depth: 0, Topic: "root", Scope: "b-02", Status: BranchOpen}
	existing := []Branch{{ID: "b1", TaskID: "task-1", Depth: 1, Topic: "web.oa", Scope: "b-02", Status: BranchOpen}}
	_, err := ValidateBranchProposal(BranchProposal{
		ParentID: root.ID, Topic: " web.oa ", Goal: "find OA again", Hypothesis: "OA is exposed",
		Target: "b-02", SuccessCondition: "OA login page", FailureCondition: "no OA page",
	}, &root, []Target{{ID: "b-02", Addresses: []string{"10.0.0.1:80"}}}, existing, DefaultExplorationPolicy())
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("ValidateBranchProposal error = %v, want duplicate error", err)
	}
}

func TestValidateKnowledgeProposalRequiresEvidence(t *testing.T) {
	branch := Branch{ID: "b1", TaskID: "task-1", Topic: "web.oa", Scope: "b-02"}
	_, err := ValidateKnowledgeProposal(KnowledgeProposal{
		Topic: "web.oa", Kind: KnowledgeConstraint, Statement: "OA is not exposed on /ecology/",
		Scope: KnowledgeScopeTarget, Target: "b-02", EvidenceRefs: []string{"missing"}, Reuse: ReuseStable,
	}, branch, map[string]struct{}{}, nil)
	if err == nil || !strings.Contains(err.Error(), "evidence") {
		t.Fatalf("ValidateKnowledgeProposal error = %v, want evidence error", err)
	}
}

func TestValidateKnowledgeProposalRejectsSecretValue(t *testing.T) {
	branch := Branch{ID: "b1", TaskID: "task-1", Topic: "web.oa", Scope: "b-02"}
	_, err := ValidateKnowledgeProposal(KnowledgeProposal{
		Topic: "web.oa", Kind: KnowledgeAnchor, Statement: "password=SuperSecret and flag{abc}",
		Scope: KnowledgeScopeTarget, Target: "b-02", EvidenceRefs: []string{"obs-1"}, Reuse: ReuseInstance,
	}, branch, map[string]struct{}{"obs-1": {}}, nil)
	if err == nil || !strings.Contains(err.Error(), "secret") {
		t.Fatalf("ValidateKnowledgeProposal error = %v, want secret error", err)
	}
}

func TestKnowledgeFingerprintUsesTopicScopeAndKind(t *testing.T) {
	left := Knowledge{Topic: " web.oa ", Scope: KnowledgeScopeTarget, Kind: KnowledgeConstraint}
	right := Knowledge{Topic: "web.oa", Scope: KnowledgeScopeTarget, Kind: KnowledgeConstraint}
	if KnowledgeFingerprint(left) != KnowledgeFingerprint(right) {
		t.Fatalf("fingerprints differ: %q != %q", KnowledgeFingerprint(left), KnowledgeFingerprint(right))
	}
}

func TestCanExpandBranchOnlyOnLocalSuccessWithEvidence(t *testing.T) {
	branch := Branch{Status: BranchResolved}
	attempt := Attempt{Status: AttemptCompleted, Outcome: AttemptLocalSuccess, ObservationIDs: []string{"obs-1"}}
	if !CanExpandBranch(branch, attempt) {
		t.Fatal("local success with observation should expand")
	}
	attempt.Outcome = AttemptRefuted
	if CanExpandBranch(branch, attempt) {
		t.Fatal("refuted branch must not expand")
	}
	attempt.Outcome = AttemptLocalSuccess
	attempt.ObservationIDs = nil
	if CanExpandBranch(branch, attempt) {
		t.Fatal("local success without evidence must not expand")
	}
}

type childExpansionExecutor struct {
	outcome   AttemptOutcome
	proposals []BranchProposal
}

func (e *childExpansionExecutor) ID() string                  { return "executor-1" }
func (e *childExpansionExecutor) Reset(context.Context) error { return nil }
func (e *childExpansionExecutor) Attempt(_ context.Context, branch Branch, _ BranchBrief) (AttemptReport, error) {
	if branch.Depth == 1 {
		return AttemptReport{
			BranchID: branch.ID, Status: AttemptCompleted, Outcome: e.outcome,
			Observations:  []Observation{{ID: "obs-1", Kind: "http", Summary: "parent evidence", EvidenceRefs: []string{"raw-1"}}},
			ChildBranches: append([]BranchProposal(nil), e.proposals...),
		}, nil
	}
	return AttemptReport{BranchID: branch.ID, Status: AttemptCompleted, Outcome: AttemptRefuted}, nil
}

func findBranchRejection(events []BranchEvent, reason string) (map[string]any, bool) {
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

func TestBranchEngineCapsExpansionAtTwoChildren(t *testing.T) {
	target := Target{ID: "b-02", Addresses: []string{"10.0.0.1:80"}}
	proposals := []BranchProposal{
		{ParentID: "parent", Topic: "web.oa.deep", Goal: "deep OA", Hypothesis: "deep OA exists", Target: "b-02", SuccessCondition: "deep page", FailureCondition: "no deep page"},
		{ParentID: "parent", Topic: "web.oa.creds", Goal: "OA creds", Hypothesis: "creds exist", Target: "b-02", SuccessCondition: "credential path", FailureCondition: "no credential path"},
		{ParentID: "parent", Topic: "web.oa.upload", Goal: "OA upload", Hypothesis: "upload exists", Target: "b-02", SuccessCondition: "upload page", FailureCondition: "no upload page"},
	}
	executor := &childExpansionExecutor{outcome: AttemptLocalSuccess, proposals: proposals}
	var events []BranchEvent
	engine := NewBranchEngine(BranchEngineConfig{
		RunID: "run-1", Task: Task{ID: "task-1", Objective: "find flag"}, Targets: []Target{target},
		Policy: DefaultExplorationPolicy(),
		AcquireExecutors: func(context.Context, int) ([]BranchExecutor, error) {
			return []BranchExecutor{executor}, nil
		},
	}, &singleBranchPlanner{}, func(event BranchEvent) { events = append(events, event) })
	if err := engine.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var parent Branch
	for _, branch := range engine.Branches() {
		if branch.Depth == 1 && branch.Topic == "web.oa" {
			parent = branch
			break
		}
	}
	if parent.ID == "" {
		t.Fatalf("depth-1 parent branch missing: %+v", engine.Branches())
	}
	children := 0
	for _, branch := range engine.Branches() {
		if branch.ParentID == parent.ID {
			children++
		}
	}
	if children != 2 {
		t.Fatalf("children = %d, want 2: %+v", children, engine.Branches())
	}
	data, ok := findBranchRejection(events, "child_limit_exceeded")
	if !ok {
		t.Fatalf("missing child_limit_exceeded rejection: %+v", events)
	}
	if data["parent_id"] != parent.ID {
		t.Fatalf("rejection parent_id = %v, want %q", data["parent_id"], parent.ID)
	}
	if data["proposal"] != proposals[2] {
		t.Fatalf("rejection proposal = %+v, want third proposal %+v", data["proposal"], proposals[2])
	}
}

func TestBranchEngineIgnoresChildrenWithoutLocalSuccess(t *testing.T) {
	target := Target{ID: "b-02", Addresses: []string{"10.0.0.1:80"}}
	proposals := []BranchProposal{
		{ParentID: "parent", Topic: "web.oa.deep", Goal: "deep OA", Hypothesis: "deep OA exists", Target: "b-02", SuccessCondition: "deep page", FailureCondition: "no deep page"},
		{ParentID: "parent", Topic: "web.oa.creds", Goal: "OA creds", Hypothesis: "creds exist", Target: "b-02", SuccessCondition: "credential path", FailureCondition: "no credential path"},
		{ParentID: "parent", Topic: "web.oa.upload", Goal: "OA upload", Hypothesis: "upload exists", Target: "b-02", SuccessCondition: "upload page", FailureCondition: "no upload page"},
	}
	executor := &childExpansionExecutor{outcome: AttemptRefuted, proposals: proposals}
	var events []BranchEvent
	engine := NewBranchEngine(BranchEngineConfig{
		RunID: "run-1", Task: Task{ID: "task-1", Objective: "find flag"}, Targets: []Target{target},
		Policy: DefaultExplorationPolicy(),
		AcquireExecutors: func(context.Context, int) ([]BranchExecutor, error) {
			return []BranchExecutor{executor}, nil
		},
	}, &singleBranchPlanner{}, func(event BranchEvent) { events = append(events, event) })
	if err := engine.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var parent Branch
	for _, branch := range engine.Branches() {
		if branch.Depth == 1 && branch.Topic == "web.oa" {
			parent = branch
			break
		}
	}
	if parent.ID == "" {
		t.Fatalf("depth-1 parent branch missing: %+v", engine.Branches())
	}
	for _, branch := range engine.Branches() {
		if branch.ParentID == parent.ID {
			t.Fatalf("unexpected child branch: %+v", branch)
		}
	}
	data, ok := findBranchRejection(events, "expansion_not_allowed")
	if !ok {
		t.Fatalf("missing expansion_not_allowed rejection: %+v", events)
	}
	if data["parent_id"] != parent.ID || data["proposal"] != proposals[0] {
		t.Fatalf("rejection data = %+v, want parent %q and first proposal", data, parent.ID)
	}
}

type fakeBranchPlanner struct {
	outputs []BranchPlanOutput
	calls   int
}

func (p *fakeBranchPlanner) Plan(context.Context, BranchPlanRequest) (BranchPlanOutput, error) {
	p.calls++
	if len(p.outputs) == 0 {
		return BranchPlanOutput{}, nil
	}
	out := p.outputs[0]
	p.outputs = p.outputs[1:]
	return out, nil
}

type fakeBranchExecutor struct {
	id string
}

func (e *fakeBranchExecutor) ID() string                  { return e.id }
func (e *fakeBranchExecutor) Reset(context.Context) error { return nil }
func (e *fakeBranchExecutor) Attempt(_ context.Context, branch Branch, _ BranchBrief) (AttemptReport, error) {
	if branch.Topic == "web.oa" {
		return AttemptReport{
			BranchID: branch.ID, Status: AttemptCompleted, Outcome: AttemptLocalSuccess, ObservationIDs: []string{"obs-1"},
			ChildBranches: []BranchProposal{
				{ParentID: branch.ID, Topic: "web.oa.deep", Goal: "deep OA", Hypothesis: "OA child exists", Target: "b-02", SuccessCondition: "child", FailureCondition: "no child"},
				{ParentID: branch.ID, Topic: "web.oa.creds", Goal: "OA creds", Hypothesis: "creds exist", Target: "b-02", SuccessCondition: "cred", FailureCondition: "no cred"},
			},
		}, nil
	}
	return AttemptReport{BranchID: branch.ID, Status: AttemptCompleted, Outcome: AttemptRefuted}, nil
}

type memoryKnowledgeStore struct {
	items []Knowledge
}

func (s *memoryKnowledgeStore) ListKnowledge(context.Context, string) ([]Knowledge, error) {
	return append([]Knowledge(nil), s.items...), nil
}
func (s *memoryKnowledgeStore) SaveKnowledge(_ context.Context, knowledge Knowledge) error {
	s.items = append(s.items, knowledge)
	return nil
}
func (s *memoryKnowledgeStore) UpdateKnowledge(_ context.Context, knowledge Knowledge) error {
	for i := range s.items {
		if s.items[i].ID == knowledge.ID {
			s.items[i] = knowledge
			return nil
		}
	}
	s.items = append(s.items, knowledge)
	return nil
}

type treePlanner struct{ calls int }

func (p *treePlanner) Plan(context.Context, BranchPlanRequest) (BranchPlanOutput, error) {
	p.calls++
	if p.calls == 1 {
		return BranchPlanOutput{CreateBranches: []BranchProposal{
			{ParentID: "root", Topic: "web.oa", Goal: "find OA", Hypothesis: "OA is exposed", Target: "b-02", SuccessCondition: "OA page", FailureCondition: "no OA page"},
			{ParentID: "root", Topic: "web.alt", Goal: "find alternate", Hypothesis: "alternate path exists", Target: "b-02", SuccessCondition: "alternate page", FailureCondition: "no alternate page"},
		}}, nil
	}
	return BranchPlanOutput{}, nil
}

func TestBranchEngineSplitsRootAndExpandsOnlySuccessfulBranch(t *testing.T) {
	target := Target{ID: "b-02", Addresses: []string{"10.0.0.1:80"}}
	planner := &treePlanner{}
	executor := &fakeBranchExecutor{id: "executor-1"}
	store := &memoryKnowledgeStore{}
	engine := NewBranchEngine(BranchEngineConfig{
		RunID: "run-1", Task: Task{ID: "task-1", Objective: "find flag"}, Targets: []Target{target},
		Policy: DefaultExplorationPolicy(), KnowledgeStore: store,
		AcquireExecutors: func(context.Context, int) ([]BranchExecutor, error) {
			return []BranchExecutor{executor}, nil
		},
	}, planner, nil)
	if err := engine.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	branches := engine.Branches()
	if len(branches) != 5 {
		t.Fatalf("branches = %d (%+v), want root + 2 + 2", len(branches), branches)
	}
	var expanded, failed int
	for _, branch := range branches {
		if branch.ParentID != "" && branch.Depth == 1 {
			if branch.Depth == 1 && branch.Status == BranchResolved {
				expanded++
			}
			if branch.Status == BranchRefuted {
				failed++
			}
		}
	}
	if expanded != 1 || failed != 1 {
		t.Fatalf("depth1 statuses resolved=%d refuted=%d, want 1/1: %+v", expanded, failed, branches)
	}
	if engine.Success() {
		t.Fatal("local branch success must not establish run success")
	}
}

type memoryExplorationStore struct {
	memoryKnowledgeStore
	branches     int
	attempts     int
	observations int
}

func (s *memoryExplorationStore) SaveBranch(context.Context, Branch) error {
	s.branches++
	return nil
}
func (s *memoryExplorationStore) SaveAttempt(context.Context, Attempt) error {
	s.attempts++
	return nil
}
func (s *memoryExplorationStore) SaveObservation(context.Context, Observation) error {
	s.observations++
	return nil
}

func TestBranchEnginePersistsNormalizedState(t *testing.T) {
	target := Target{ID: "b-02", Addresses: []string{"10.0.0.1:80"}}
	planner := &singleBranchPlanner{}
	executor := &persistingExecutor{}
	store := &memoryExplorationStore{}
	engine := NewBranchEngine(BranchEngineConfig{
		RunID: "run-1", Task: Task{ID: "task-1", Objective: "find flag"}, Targets: []Target{target},
		Policy: DefaultExplorationPolicy(), Store: store,
		AcquireExecutors: func(context.Context, int) ([]BranchExecutor, error) {
			return []BranchExecutor{executor}, nil
		},
	}, planner, nil)
	if err := engine.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if store.branches == 0 || store.attempts == 0 || store.observations == 0 {
		t.Fatalf("persisted counts = branches %d attempts %d observations %d", store.branches, store.attempts, store.observations)
	}
}

type singleBranchPlanner struct{ called bool }

func (p *singleBranchPlanner) Plan(context.Context, BranchPlanRequest) (BranchPlanOutput, error) {
	if p.called {
		return BranchPlanOutput{}, nil
	}
	p.called = true
	return BranchPlanOutput{CreateBranches: []BranchProposal{{
		ParentID: "root", Topic: "web.oa", Goal: "find OA", Hypothesis: "OA exists",
		Target: "b-02", SuccessCondition: "OA found", FailureCondition: "no OA",
	}}}, nil
}

type persistingExecutor struct{}

func (e *persistingExecutor) ID() string                  { return "executor-1" }
func (e *persistingExecutor) Reset(context.Context) error { return nil }
func (e *persistingExecutor) Attempt(_ context.Context, branch Branch, _ BranchBrief) (AttemptReport, error) {
	return AttemptReport{
		BranchID: branch.ID, Status: AttemptCompleted, Outcome: AttemptLocalSuccess,
		Observations: []Observation{{ID: "obs-1", Kind: "http", Summary: "OA found", EvidenceRefs: []string{"raw-1"}}},
	}, nil
}

type inconclusiveExecutor struct{}

func (e *inconclusiveExecutor) ID() string                  { return "executor-1" }
func (e *inconclusiveExecutor) Reset(context.Context) error { return nil }
func (e *inconclusiveExecutor) Attempt(_ context.Context, branch Branch, _ BranchBrief) (AttemptReport, error) {
	return AttemptReport{BranchID: branch.ID, Status: AttemptCompleted, Outcome: AttemptInconclusive}, nil
}

func TestBranchEngineStagnatesWithoutProgressOrKnowledge(t *testing.T) {
	target := Target{ID: "b-02", Addresses: []string{"10.0.0.1:80"}}
	policy := DefaultExplorationPolicy()
	policy.StagnationRounds = 1
	engine := NewBranchEngine(BranchEngineConfig{
		RunID: "run-1", Task: Task{ID: "task-1", Objective: "find flag"}, Targets: []Target{target},
		Policy: policy,
		AcquireExecutors: func(context.Context, int) ([]BranchExecutor, error) {
			return []BranchExecutor{&inconclusiveExecutor{}}, nil
		},
	}, &singleBranchPlanner{}, nil)
	if err := engine.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !engine.Stagnated() {
		t.Fatal("engine did not report stagnation")
	}
}

type conflictExecutor struct{}

func (e *conflictExecutor) ID() string                  { return "executor-1" }
func (e *conflictExecutor) Reset(context.Context) error { return nil }
func (e *conflictExecutor) Attempt(_ context.Context, branch Branch, _ BranchBrief) (AttemptReport, error) {
	if branch.Topic == "web.oa" {
		return AttemptReport{
			BranchID: branch.ID, Status: AttemptCompleted, Outcome: AttemptLocalSuccess,
			Observations: []Observation{{ID: "obs-1", Kind: "http", Summary: "new evidence", EvidenceRefs: []string{"raw-1"}}},
			Knowledge: []KnowledgeProposal{{
				Topic: "web.oa.alt", Kind: KnowledgeAnchor, Statement: "alternate OA path exists",
				Scope: KnowledgeScopeTarget, Target: "b-02", EvidenceRefs: []string{"obs-1"},
				Refutes: []string{"k-old"}, Reuse: ReuseStable,
			}},
		}, nil
	}
	return AttemptReport{BranchID: branch.ID, Status: AttemptCompleted, Outcome: AttemptRefuted}, nil
}

func TestBranchEngineMarksConflictingKnowledgeContested(t *testing.T) {
	target := Target{ID: "b-02", Addresses: []string{"10.0.0.1:80"}}
	store := &memoryExplorationStore{}
	store.items = append(store.items, Knowledge{
		ID: "k-old", TaskID: "task-1", Topic: "web.oa", Kind: KnowledgeAnchor,
		Statement: "OA path exists", Scope: KnowledgeScopeTarget, Trust: KnowledgeObserved, Reuse: ReuseStable,
	})
	engine := NewBranchEngine(BranchEngineConfig{
		RunID: "run-1", Task: Task{ID: "task-1", Objective: "find flag"}, Targets: []Target{target},
		Policy: DefaultExplorationPolicy(), KnowledgeStore: store,
		AcquireExecutors: func(context.Context, int) ([]BranchExecutor, error) {
			return []BranchExecutor{&conflictExecutor{}}, nil
		},
	}, &singleBranchPlanner{}, nil)
	if err := engine.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(store.items) < 2 {
		t.Fatalf("knowledge = %+v, want old and new", store.items)
	}
	if store.items[0].Trust != KnowledgeContested {
		t.Fatalf("old trust = %q, want contested", store.items[0].Trust)
	}
	if store.items[1].Trust != KnowledgeContested {
		t.Fatalf("new trust = %q, want contested", store.items[1].Trust)
	}
	var review bool
	for _, branch := range engine.Branches() {
		if branch.Topic == "review.web.oa.alt" {
			review = true
		}
	}
	if !review {
		t.Fatalf("review branch missing: %+v", engine.Branches())
	}
}

func TestBranchEngineExemptsReviewBranchesFromChildCap(t *testing.T) {
	target := Target{ID: "b-02", Addresses: []string{"10.0.0.1:80"}}
	policy := DefaultExplorationPolicy()
	policy.MaxChildrenPerBranch = 2
	var events []BranchEvent
	engine := NewBranchEngine(BranchEngineConfig{
		RunID: "run-1", Task: Task{ID: "task-1", Objective: "find flag"}, Targets: []Target{target}, Policy: policy,
	}, nil, func(event BranchEvent) { events = append(events, event) })
	parent := Branch{
		ID: "branch-parent", TaskID: "task-1", RunID: "run-1", Depth: 1,
		Topic: "web.oa", Scope: "b-02", Status: BranchResolved,
	}
	engine.branches = append(engine.branches,
		Branch{ID: "root", TaskID: "task-1", RunID: "run-1", Depth: 0, Topic: "root", Scope: "b-02", Status: BranchResolved},
		parent,
	)
	engine.addReviewBranch(parent, Knowledge{Topic: "web.oa.alt", Scope: KnowledgeScopeTarget})

	reviewIndex := -1
	var review Branch
	for index, branch := range engine.branches {
		if strings.HasPrefix(branch.Topic, "review.") {
			reviewIndex = index
			review = branch
			break
		}
	}
	if reviewIndex < 0 {
		t.Fatalf("review branch missing: %+v", engine.Branches())
	}
	proposals := []BranchProposal{
		{ParentID: review.ID, Topic: "review.one", Goal: "review one", Hypothesis: "one is supportable", Target: "b-02", SuccessCondition: "one supported", FailureCondition: "one unsupported"},
		{ParentID: review.ID, Topic: "review.two", Goal: "review two", Hypothesis: "two is supportable", Target: "b-02", SuccessCondition: "two supported", FailureCondition: "two unsupported"},
		{ParentID: review.ID, Topic: "review.three", Goal: "review three", Hypothesis: "three is supportable", Target: "b-02", SuccessCondition: "three supported", FailureCondition: "three unsupported"},
	}
	engine.expandChildren(reviewIndex, Attempt{
		ID: "attempt-review", BranchID: review.ID, Status: AttemptCompleted,
		Outcome: AttemptLocalSuccess, ObservationIDs: []string{"obs-review"},
	}, proposals)

	children := 0
	for _, branch := range engine.Branches() {
		if branch.ParentID == review.ID {
			children++
		}
	}
	if children != len(proposals) {
		t.Fatalf("review children = %d, want %d: %+v", children, len(proposals), engine.Branches())
	}
	if _, rejected := findBranchRejection(events, "child_limit_exceeded"); rejected {
		t.Fatalf("review branch was capped: %+v", events)
	}
}
