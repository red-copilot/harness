package harness

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ProgressSignal is an authoritative progress observation produced by a
// Backend or Verifier. Model output can never construct an authoritative one.
type ProgressSignal struct {
	Authoritative bool    `json:"authoritative"`
	Source        string  `json:"source,omitempty"`
	Done          bool    `json:"done,omitempty"`
	Success       bool    `json:"success,omitempty"`
	Progress      int     `json:"progress,omitempty"`
	Total         int     `json:"total,omitempty"`
	Score         float64 `json:"score,omitempty"`
	Message       string  `json:"message,omitempty"`
}

// BranchPlanner proposes branches. It is read-only and has no tools.
type BranchPlanner interface {
	Plan(context.Context, BranchPlanRequest) (BranchPlanOutput, error)
}

// BranchExecutor executes exactly one bounded Branch attempt.
type BranchExecutor interface {
	ID() string
	Reset(context.Context) error
	Attempt(context.Context, Branch, BranchBrief) (AttemptReport, error)
}

// KnowledgeStore persists task-scoped durable knowledge.
type KnowledgeStore interface {
	ListKnowledge(context.Context, string) ([]Knowledge, error)
	SaveKnowledge(context.Context, Knowledge) error
	UpdateKnowledge(context.Context, Knowledge) error
}

// BranchStore persists normalized branch, attempt, and observation state.
type BranchStore interface {
	SaveBranch(context.Context, Branch) error
	SaveAttempt(context.Context, Attempt) error
	SaveObservation(context.Context, Observation) error
}

// BranchPlanRequest is the bounded planner context.
type BranchPlanRequest struct {
	Task      Task
	Targets   []Target
	Branches  []Branch
	Knowledge []Knowledge
	Progress  ProgressSignal
	Reports   []AttemptReport
}

// BranchPlanOutput is the planner's proposal. The engine owns validation.
type BranchPlanOutput struct {
	CreateBranches []BranchProposal `json:"create_branches"`
	CloseBranches  []string         `json:"close_branches,omitempty"`
	Priorities     map[string]int   `json:"priorities,omitempty"`
}

// BranchBrief is the complete, bounded context for one executor attempt.
type BranchBrief struct {
	Objective string
	Branch    Branch
	Knowledge []Knowledge
	Budget    BranchBudget
}

// AttemptReport is the structured result of one branch attempt.
type AttemptReport struct {
	BranchID       string              `json:"branch_id"`
	Status         AttemptStatus       `json:"status"`
	Outcome        AttemptOutcome      `json:"outcome"`
	ObservationIDs []string            `json:"observation_ids,omitempty"`
	Observations   []Observation       `json:"observations,omitempty"`
	Knowledge      []KnowledgeProposal `json:"knowledge,omitempty"`
	ChildBranches  []BranchProposal    `json:"child_branches,omitempty"`
	Usage          Usage               `json:"usage,omitempty"`
	Error          string              `json:"error,omitempty"`
}

// BranchEvent is a bounded, structured exploration transition.
type BranchEvent struct {
	Type string
	Data any
}

// BranchEngineConfig configures one run-scoped binary hypothesis tree.
type BranchEngineConfig struct {
	RunID            string
	Task             Task
	Targets          []Target
	Policy           ExplorationPolicy
	KnowledgeStore   KnowledgeStore
	Store            BranchStore
	Progress         func() ProgressSignal
	OnStagnated      func()
	AcquireExecutors func(context.Context, int) ([]BranchExecutor, error)
}

// BranchEngine is the deterministic owner of the v2 exploration tree.
type BranchEngine struct {
	cfg     BranchEngineConfig
	planner BranchPlanner
	emit    func(BranchEvent)

	branches       []Branch
	attempts       []Attempt
	observations   []Observation
	knowledge      []Knowledge
	reports        []AttemptReport
	success        bool
	stagnated      bool
	stagnantRounds int
	lastProgress   int
	lastKnowledge  int
	needPlan       bool
	reviewBranches map[string]struct{}
}

// NewBranchEngine creates a v2 tree engine.
func NewBranchEngine(cfg BranchEngineConfig, planner BranchPlanner, emit func(BranchEvent)) *BranchEngine {
	cfg.Policy = NormalizeExplorationPolicy(cfg.Policy)
	if emit == nil {
		emit = func(BranchEvent) {}
	}
	return &BranchEngine{
		cfg: cfg, planner: planner, emit: emit, needPlan: true,
		reviewBranches: make(map[string]struct{}),
	}
}

// Run executes the binary tree until progress, exhaustion, or context stop.
func (e *BranchEngine) Run(ctx context.Context) error {
	if e.planner == nil {
		return fmt.Errorf("branch engine requires a planner")
	}
	if e.cfg.AcquireExecutors == nil {
		return fmt.Errorf("branch engine requires executor acquisition")
	}
	if e.cfg.KnowledgeStore != nil && strings.TrimSpace(e.cfg.Task.ID) != "" {
		items, err := e.cfg.KnowledgeStore.ListKnowledge(ctx, e.cfg.Task.ID)
		if err != nil {
			return fmt.Errorf("load task knowledge: %w", err)
		}
		e.knowledge = append(e.knowledge, items...)
	}
	root := Branch{
		ID: "root", TaskID: e.cfg.Task.ID, RunID: e.cfg.RunID, Depth: 0,
		Topic: "root", Goal: e.cfg.Task.Objective, Status: BranchOpen,
		Scope: firstTargetScope(e.cfg.Targets), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	e.branches = append(e.branches, root)
	e.saveBranch(root)
	e.emit(BranchEvent{Type: "branch.created", Data: root})

	maxRounds := e.cfg.Policy.MaxNodes
	if maxRounds <= 0 {
		maxRounds = DefaultExplorationPolicy().MaxNodes
	}
	for round := 0; round < maxRounds; round++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		progress := e.progress()
		if progress.Done {
			e.success = progress.Success
			e.emit(BranchEvent{Type: "progress.updated", Data: progress})
			return nil
		}
		if e.needPlan {
			if err := e.plan(ctx, round); err != nil {
				return err
			}
			e.needPlan = false
		}
		ready := e.readyBranches()
		if len(ready) == 0 {
			return nil
		}
		if err := e.runBranches(ctx, round, ready); err != nil {
			return err
		}
		e.observeRound()
		if e.stagnated {
			e.emit(BranchEvent{Type: "stagnated", Data: map[string]any{"round": round + 1}})
			if e.cfg.OnStagnated != nil {
				e.cfg.OnStagnated()
			}
			return nil
		}
	}
	return nil
}

func (e *BranchEngine) plan(ctx context.Context, round int) error {
	request := BranchPlanRequest{
		Task: e.cfg.Task, Targets: append([]Target(nil), e.cfg.Targets...),
		Branches: e.Branches(), Knowledge: append([]Knowledge(nil), e.knowledge...),
		Progress: e.progress(), Reports: append([]AttemptReport(nil), e.reports...),
	}
	output, err := e.planner.Plan(ctx, request)
	if err != nil {
		return fmt.Errorf("branch planner: %w", err)
	}
	for _, id := range output.CloseBranches {
		for i := range e.branches {
			if e.branches[i].ID == id {
				e.branches[i].Status = BranchAbandoned
				e.branches[i].UpdatedAt = time.Now().UTC()
				e.emit(BranchEvent{Type: "branch.closed", Data: e.branches[i]})
			}
		}
	}
	for id, priority := range output.Priorities {
		for i := range e.branches {
			if e.branches[i].ID == id {
				e.branches[i].Priority = priority
			}
		}
	}
	for _, proposal := range output.CreateBranches {
		parentID := strings.TrimSpace(proposal.ParentID)
		if parentID == "" {
			parentID = "root"
		}
		if parentID != "root" {
			e.rejectBranch(parentID, "planner_cannot_expand", proposal)
			continue
		}
		parent := e.findBranch(parentID)
		if parent == nil {
			continue
		}
		branch, err := ValidateBranchProposal(proposal, parent, e.cfg.Targets, e.branches, e.cfg.Policy)
		if err != nil {
			e.rejectBranch(parent.ID, err.Error(), proposal)
			continue
		}
		if parent.Status == BranchOpen {
			parent.Status = BranchResolved
			parent.UpdatedAt = time.Now().UTC()
		}
		e.branches = append(e.branches, branch)
		e.emit(BranchEvent{Type: "branch.created", Data: branch})
	}
	return nil
}

func (e *BranchEngine) runBranches(ctx context.Context, round int, ready []Branch) error {
	limit := e.cfg.Policy.MaxActiveBranches
	if limit <= 0 || limit > len(ready) {
		limit = len(ready)
	}
	executors, err := e.cfg.AcquireExecutors(ctx, limit)
	if err != nil {
		return fmt.Errorf("acquire branch executors: %w", err)
	}
	if len(executors) == 0 {
		return fmt.Errorf("no branch executors available")
	}
	for i := 0; i < limit; i++ {
		branchIndex := e.indexBranch(ready[i].ID)
		if branchIndex < 0 {
			continue
		}
		executor := executors[i%len(executors)]
		e.branches[branchIndex].Status = BranchActive
		attempt := Attempt{
			ID:       fmt.Sprintf("attempt-%d-%s", round+1, shortHash(ready[i].ID)[:8]),
			BranchID: ready[i].ID, RunID: e.cfg.RunID, SessionID: executor.ID(),
			Status: AttemptRunning, StartedAt: time.Now().UTC(),
		}
		e.saveAttempt(attempt)
		e.emit(BranchEvent{Type: "attempt.started", Data: attempt})
		if err := executor.Reset(ctx); err != nil {
			attempt.Status = AttemptFailed
			attempt.Outcome = AttemptOperationalFailure
			attempt.Error = err.Error()
			attempt.FinishedAt = time.Now().UTC()
			e.attempts = append(e.attempts, attempt)
			e.branches[branchIndex].Status = BranchPaused
			e.emit(BranchEvent{Type: "attempt.failed", Data: attempt})
			continue
		}
		brief := BranchBrief{Objective: e.cfg.Task.Objective, Branch: ready[i], Knowledge: e.knowledge, Budget: e.branchBudget()}
		report, err := executor.Attempt(ctx, ready[i], brief)
		if err != nil {
			attempt.Status = AttemptFailed
			attempt.Outcome = AttemptOperationalFailure
			attempt.Error = err.Error()
			attempt.FinishedAt = time.Now().UTC()
			e.attempts = append(e.attempts, attempt)
			e.branches[branchIndex].Status = BranchPaused
			e.emit(BranchEvent{Type: "attempt.failed", Data: attempt})
			continue
		}
		if report.BranchID == "" {
			report.BranchID = ready[i].ID
		}
		if report.BranchID != ready[i].ID {
			attempt.Status = AttemptFailed
			attempt.Outcome = AttemptOperationalFailure
			attempt.Error = fmt.Sprintf("attempt branch_id %q does not match %q", report.BranchID, ready[i].ID)
			attempt.FinishedAt = time.Now().UTC()
			e.attempts = append(e.attempts, attempt)
			e.branches[branchIndex].Status = BranchPaused
			e.emit(BranchEvent{Type: "attempt.failed", Data: attempt})
			continue
		}
		if report.Status == "" {
			report.Status = AttemptCompleted
		}
		if report.Outcome == "" {
			report.Outcome = AttemptInconclusive
		}
		attempt.Status = report.Status
		attempt.Outcome = report.Outcome
		attempt.ObservationIDs = append([]string(nil), report.ObservationIDs...)
		attempt.FinishedAt = time.Now().UTC()
		e.attempts = append(e.attempts, attempt)
		e.saveAttempt(attempt)
		e.recordObservations(attempt.ID, report.Observations, &attempt)
		e.reports = append(e.reports, report)
		if attempt.Status == AttemptCompleted {
			e.promoteKnowledge(report, e.branches[branchIndex])
		}
		e.applyOutcome(branchIndex, attempt)
		e.expandChildren(branchIndex, attempt, report.ChildBranches)
		e.needPlan = true
		e.saveBranch(e.branches[branchIndex])
		e.saveAttempt(attempt)
		e.emit(BranchEvent{Type: "attempt.completed", Data: attempt})
	}
	return nil
}

func (e *BranchEngine) expandChildren(parentIndex int, attempt Attempt, proposals []BranchProposal) {
	if len(proposals) == 0 {
		return
	}
	if parentIndex < 0 || parentIndex >= len(e.branches) {
		return
	}
	parent := &e.branches[parentIndex]
	if !CanExpandBranch(*parent, attempt) {
		e.rejectBranch(parent.ID, "expansion_not_allowed", proposals[0])
		return
	}
	created := 0
	_, reviewBranch := e.reviewBranches[parent.ID]
	for _, proposal := range proposals {
		if !reviewBranch && created >= e.cfg.Policy.MaxChildrenPerBranch {
			e.rejectBranch(parent.ID, "child_limit_exceeded", proposal)
			continue
		}
		branch, err := ValidateBranchProposal(proposal, parent, e.cfg.Targets, e.branches, e.cfg.Policy)
		if err != nil {
			e.rejectBranch(parent.ID, err.Error(), proposal)
			continue
		}
		e.branches = append(e.branches, branch)
		created++
		e.saveBranch(branch)
		e.emit(BranchEvent{Type: "branch.created", Data: branch})
	}
}

func (e *BranchEngine) rejectBranch(parentID, reason string, proposal BranchProposal) {
	e.emit(BranchEvent{Type: "exploration.branch.rejected", Data: map[string]any{
		"parent_id": parentID,
		"reason":    reason,
		"proposal":  proposal,
	}})
}

func (e *BranchEngine) recordObservations(attemptID string, observations []Observation, attempt *Attempt) {
	for i := range observations {
		if observations[i].ID == "" {
			observations[i].ID = fmt.Sprintf("%s-obs-%d", attemptID, i+1)
		}
		observations[i].AttemptID = attemptID
		if observations[i].CreatedAt.IsZero() {
			observations[i].CreatedAt = time.Now().UTC()
		}
		e.observations = append(e.observations, observations[i])
		attempt.ObservationIDs = append(attempt.ObservationIDs, observations[i].ID)
		e.saveObservation(observations[i])
		e.emit(BranchEvent{Type: "observation.recorded", Data: observations[i]})
	}
}

func (e *BranchEngine) promoteKnowledge(report AttemptReport, branch Branch) {
	known := make(map[string]struct{}, len(e.observations))
	for _, observation := range e.observations {
		known[observation.ID] = struct{}{}
	}
	for _, proposal := range report.Knowledge {
		knowledge, err := ValidateKnowledgeProposal(proposal, branch, known, e.knowledge)
		if err != nil {
			e.emit(BranchEvent{Type: "knowledge.rejected", Data: map[string]any{"proposal": proposal, "error": err.Error()}})
			continue
		}
		if len(proposal.Refutes) > 0 {
			knowledge.Trust = KnowledgeContested
			for _, refutedID := range proposal.Refutes {
				for i := range e.knowledge {
					if e.knowledge[i].ID != refutedID {
						continue
					}
					e.knowledge[i].Trust = KnowledgeContested
					e.knowledge[i].Contradicts = appendUnique(e.knowledge[i].Contradicts, knowledge.ID)
					if e.cfg.KnowledgeStore != nil {
						_ = e.cfg.KnowledgeStore.UpdateKnowledge(context.Background(), e.knowledge[i])
					}
					e.emit(BranchEvent{Type: "knowledge.contested", Data: e.knowledge[i]})
				}
			}
			e.addReviewBranch(branch, knowledge)
		}
		e.knowledge = append(e.knowledge, knowledge)
		if e.cfg.KnowledgeStore != nil && strings.TrimSpace(knowledge.TaskID) != "" {
			_ = e.cfg.KnowledgeStore.SaveKnowledge(context.Background(), knowledge)
		}
		e.emit(BranchEvent{Type: "knowledge.promoted", Data: knowledge})
	}
}

func (e *BranchEngine) applyOutcome(index int, attempt Attempt) {
	switch attempt.Outcome {
	case AttemptLocalSuccess:
		e.branches[index].Status = BranchResolved
	case AttemptRefuted:
		e.branches[index].Status = BranchRefuted
	case AttemptBlocked:
		e.branches[index].Status = BranchBlocked
	case AttemptOperationalFailure:
		if e.attemptCount(e.branches[index].ID) < 2 {
			e.branches[index].Status = BranchOpen
		} else {
			e.branches[index].Status = BranchBlocked
		}
	default:
		if e.attemptCount(e.branches[index].ID) >= e.cfg.Policy.MaxAttemptsPerBranch {
			e.branches[index].Status = BranchBlocked
		} else {
			e.branches[index].Status = BranchOpen
		}
	}
	e.branches[index].UpdatedAt = time.Now().UTC()
}

func (e *BranchEngine) readyBranches() []Branch {
	var out []Branch
	for _, branch := range e.branches {
		if branch.Topic != "root" && branch.Status == BranchOpen {
			out = append(out, branch)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		if out[i].Depth != out[j].Depth {
			return out[i].Depth < out[j].Depth
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (e *BranchEngine) findBranch(id string) *Branch {
	for i := range e.branches {
		if e.branches[i].ID == id {
			return &e.branches[i]
		}
	}
	return nil
}

func (e *BranchEngine) indexBranch(id string) int {
	for i := range e.branches {
		if e.branches[i].ID == id {
			return i
		}
	}
	return -1
}

func (e *BranchEngine) attemptCount(branchID string) int {
	count := 0
	for _, attempt := range e.attempts {
		if attempt.BranchID == branchID {
			count++
		}
	}
	return count
}

func (e *BranchEngine) addReviewBranch(parent Branch, knowledge Knowledge) {
	topic := "review." + normalizeTopic(knowledge.Topic)
	for _, branch := range e.branches {
		if branch.Topic == topic && branch.Scope == parent.Scope {
			return
		}
	}
	if parent.Depth+1 > e.cfg.Policy.MaxDepth || len(e.branches)+1 > e.cfg.Policy.MaxNodes {
		return
	}
	now := time.Now().UTC()
	branch := Branch{
		ID:       "branch-" + shortHash(topic + "\x00" + parent.Scope)[:16],
		ParentID: parent.ID, TaskID: parent.TaskID, RunID: parent.RunID,
		Depth: parent.Depth + 1, Topic: topic, Goal: "复核冲突知识",
		Hypothesis:       "冲突双方中至少一方可以被独立证据支持或反驳",
		SuccessCondition: "得到独立证据支持其中一方", FailureCondition: "无法获得独立证据",
		Scope: parent.Scope, Status: BranchOpen, CreatedAt: now, UpdatedAt: now,
	}
	e.branches = append(e.branches, branch)
	if e.reviewBranches == nil {
		e.reviewBranches = make(map[string]struct{})
	}
	e.reviewBranches[branch.ID] = struct{}{}
	e.saveBranch(branch)
	e.emit(BranchEvent{Type: "branch.created", Data: branch})
}

func (e *BranchEngine) observeRound() {
	progress := e.progress()
	progressChanged := progress.Progress > e.lastProgress || (progress.Done && progress.Success)
	knowledgeChanged := len(e.knowledge) > e.lastKnowledge
	if progressChanged || knowledgeChanged {
		e.stagnantRounds = 0
		e.lastProgress = progress.Progress
		e.lastKnowledge = len(e.knowledge)
		return
	}
	e.stagnantRounds++
	if e.stagnantRounds >= e.cfg.Policy.StagnationRounds {
		e.stagnated = true
	}
}

func (e *BranchEngine) saveBranch(branch Branch) {
	if e.cfg.Store != nil {
		_ = e.cfg.Store.SaveBranch(context.Background(), branch)
	}
}

func (e *BranchEngine) saveAttempt(attempt Attempt) {
	if e.cfg.Store != nil {
		_ = e.cfg.Store.SaveAttempt(context.Background(), attempt)
	}
}

func (e *BranchEngine) saveObservation(observation Observation) {
	if e.cfg.Store != nil {
		_ = e.cfg.Store.SaveObservation(context.Background(), observation)
	}
}

func (e *BranchEngine) progress() ProgressSignal {
	if e.cfg.Progress == nil {
		return ProgressSignal{}
	}
	return e.cfg.Progress()
}

func (e *BranchEngine) branchBudget() BranchBudget {
	return BranchBudget{
		MaxAttempts:  e.cfg.Policy.MaxAttemptsPerBranch,
		MaxToolCalls: e.cfg.Policy.MaxToolCallsPerAttempt,
		MaxWallTime:  e.cfg.Policy.MaxWallTimePerAttempt,
	}
}

// Branches returns a copy of the current tree.
func (e *BranchEngine) Branches() []Branch {
	return append([]Branch(nil), e.branches...)
}

// Attempts returns a copy of all attempts.
func (e *BranchEngine) Attempts() []Attempt {
	return append([]Attempt(nil), e.attempts...)
}

// Observations returns a copy of all observations.
func (e *BranchEngine) Observations() []Observation {
	return append([]Observation(nil), e.observations...)
}

// Knowledge returns a copy of all promoted knowledge.
func (e *BranchEngine) Knowledge() []Knowledge {
	return append([]Knowledge(nil), e.knowledge...)
}

// Success reports whether an authoritative progress signal established success.
func (e *BranchEngine) Success() bool { return e.success }

// Stagnated reports whether the tree stopped without progress or new knowledge.
func (e *BranchEngine) Stagnated() bool { return e.stagnated }

func firstTargetScope(targets []Target) string {
	if len(targets) == 0 {
		return ""
	}
	if targets[0].ID != "" {
		return targets[0].ID
	}
	if len(targets[0].Addresses) > 0 {
		return targets[0].Addresses[0]
	}
	return ""
}
