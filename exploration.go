package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrBranchRejected marks a branch proposal refused by tree limits. It is a
// bounded-tree outcome, not a canonical failure.
var ErrBranchRejected = errors.New("branch rejected")

type explorationState struct {
	runID        string
	task         Task
	target       Target
	policy       ExplorationPolicy
	branches     []Branch
	attempts     []Attempt
	observations []Observation
	knowledge    []Knowledge
	artifacts    []Artifact
	lastBatch    []AttemptSummary
	seq          int
}

type attemptExecution struct {
	branch    Branch
	session   AgentSession
	sessionID string
	attempt   Attempt
	report    AttemptReport
	err       error
}

type explorationEngine struct {
	runner    *Runner
	live      *Run
	spec      RunSpec
	state     *explorationState
	planner   AgentSession
	executors []AgentSession
}

func newExplorationState(runID string, spec RunSpec) *explorationState {
	return &explorationState{runID: runID, task: spec.Task, target: spec.Target, policy: NormalizeExplorationPolicy(spec.Policy)}
}

func (s *explorationState) nextID(kind string) string {
	s.seq++
	return fmt.Sprintf("%s:%s:%d", s.runID, kind, s.seq)
}

func (s *explorationState) root() Branch {
	now := time.Now().UTC()
	return Branch{
		ID: s.nextID("branch"), RunID: s.runID, TaskID: s.task.ID, Kind: BranchRoot,
		Topic: "root", Goal: s.task.Objective, Hypothesis: "root", Scope: s.target.ID,
		Status: BranchOpen, CreatedAt: now, UpdatedAt: now,
	}
}

func (s *explorationState) attemptCount(branchID string) int {
	count := 0
	for _, attempt := range s.attempts {
		if attempt.BranchID == branchID {
			count++
		}
	}
	return count
}

func (s *explorationState) childCount(parentID string) int {
	count := 0
	for _, branch := range s.branches {
		if branch.ParentID == parentID {
			count++
		}
	}
	return count
}

func (s *explorationState) findBranch(id string) *Branch {
	for i := range s.branches {
		if s.branches[i].ID == id {
			return &s.branches[i]
		}
	}
	return nil
}

func (s *explorationState) readyBranches() []Branch {
	out := make([]Branch, 0)
	for _, branch := range s.branches {
		if branch.Kind == BranchRoot || branch.Status != BranchOpen {
			continue
		}
		if s.attemptCount(branch.ID) >= s.policy.MaxAttemptsPerBranch {
			continue
		}
		out = append(out, branch)
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

func (e *explorationEngine) run(ctx context.Context) error {
	state := e.state
	root := state.root()
	state.branches = append(state.branches, root)
	if err := e.persistBranch(root); err != nil {
		return err
	}
	if err := e.emit("exploration.branch.created", root); err != nil {
		return err
	}

	reused, err := e.runner.queryReusableKnowledge(ctx, e.spec)
	if err != nil {
		return err
	}
	state.knowledge = append(state.knowledge, reused...)

	plannerSession, err := e.startSession(ctx, AgentRolePlanner, e.spec.Planner, "planner")
	if err != nil {
		return err
	}
	e.planner = plannerSession
	defer e.closeSession(plannerSession, "planner")
	if err := e.emit("agent.session.started", map[string]string{"session_id": "planner", "role": "planner"}); err != nil {
		return err
	}

	for i, spec := range e.spec.Executors {
		sessionID := fmt.Sprintf("executor-%d", i+1)
		session, err := e.startSession(ctx, AgentRoleExecutor, spec, sessionID)
		if err != nil {
			return err
		}
		e.executors = append(e.executors, session)
		if err := e.emit("agent.session.started", map[string]string{"session_id": sessionID, "role": "executor"}); err != nil {
			return err
		}
	}
	defer func() {
		for i, session := range e.executors {
			e.closeSession(session, fmt.Sprintf("executor-%d", i+1))
		}
	}()

	stagnantRounds := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.live.backendCompletion() != nil {
			return nil
		}
		added, err := e.plan(ctx, root)
		if err != nil {
			return err
		}
		ready := state.readyBranches()
		if len(ready) == 0 {
			if added == 0 {
				break
			}
			continue
		}
		limit := min(state.policy.MaxActiveBranches, len(e.executors), len(ready))
		if limit <= 0 {
			break
		}
		batch := ready[:limit]
		executions, err := e.runBatch(ctx, batch)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if e.live.backendCompletion() != nil {
			return nil
		}
		progressBefore := len(state.knowledge)
		completed := 0
		state.lastBatch = state.lastBatch[:0]
		for _, execution := range executions {
			if err := e.mergeAttempt(ctx, execution); err != nil {
				return err
			}
			state.lastBatch = append(state.lastBatch, AttemptSummary{ID: execution.attempt.ID, BranchID: execution.attempt.BranchID, Status: execution.attempt.Status, Outcome: execution.attempt.Outcome, Error: execution.attempt.Error})
			if execution.attempt.Status == AttemptCompleted {
				completed++
			}
		}
		if completed == 0 && len(state.knowledge) == progressBefore {
			stagnantRounds++
		} else {
			stagnantRounds = 0
		}
		if stagnantRounds >= state.policy.StagnationRounds {
			e.live.setStagnated()
			break
		}
	}
	return nil
}

func (e *explorationEngine) plan(ctx context.Context, root Branch) (int, error) {
	state := e.state
	request, dropped := buildPlannerContext(state.task, state.target, state.branches, state.knowledge, e.live.progress(), state.attempts, state.policy)
	if dropped > 0 {
		if err := e.emit("exploration.context.truncated", map[string]any{"context": "planner", "candidates": len(state.knowledge), "kept": len(request.Knowledge), "dropped": dropped}); err != nil {
			return 0, err
		}
	}
	strategy := e.spec.PlannerStrategy
	if strategy == nil {
		strategy = defaultPlannerStrategy{}
	}
	prompt, err := strategy.Prompt(request)
	if err != nil {
		return 0, err
	}
	planCtx := ctx
	cancel := func() {}
	if state.policy.MaxPlannerWallTime > 0 {
		planCtx, cancel = context.WithTimeout(ctx, state.policy.MaxPlannerWallTime)
	}
	defer cancel()
	if err := e.planner.Reset(planCtx); err != nil {
		return 0, fmt.Errorf("reset planner: %w", err)
	}
	turn, err := RunAgentTurn(planCtx, e.planner, prompt, TurnLimits{MaxToolCalls: state.policy.MaxPlannerTools, ToolTimeout: DefaultToolCallTimeout}, e.eventSink("planner"))
	if err != nil {
		return 0, fmt.Errorf("planner turn: %w", err)
	}
	proposal, err := strategy.Parse(request, turn)
	if err != nil {
		return 0, err
	}
	e.live.addUsage(turn.Usage)
	added := 0
	for _, candidate := range proposal.CreateBranches {
		if candidate.ParentID != root.ID && candidate.ParentID != "root" {
			if err := e.emit("exploration.branch.rejected", map[string]any{"reason": "planner_cannot_expand", "parent_id": candidate.ParentID}); err != nil {
				return added, err
			}
			continue
		}
		if err := e.addBranch(candidate, root, BranchHypothesis, nil); err != nil {
			if err := e.emit("exploration.branch.rejected", map[string]any{"reason": err.Error(), "topic": candidate.Topic}); err != nil {
				return added, err
			}
			continue
		}
		added++
	}
	for _, id := range proposal.CloseBranches {
		if branch := state.findBranch(id); branch != nil && branch.Kind != BranchRoot {
			branch.Status = BranchAbandoned
			branch.UpdatedAt = time.Now().UTC()
			if err := e.persistBranch(*branch); err != nil {
				return added, err
			}
		}
	}
	for id, priority := range proposal.Priorities {
		if branch := state.findBranch(id); branch != nil {
			branch.Priority = priority
			branch.UpdatedAt = time.Now().UTC()
			if err := e.persistBranch(*branch); err != nil {
				return added, err
			}
		}
	}
	return added, nil
}

func (e *explorationEngine) addBranch(proposal BranchProposal, parent Branch, kind BranchKind, reviewOf []string) error {
	state := e.state
	if len(state.branches) >= state.policy.MaxNodes {
		return fmt.Errorf("%w: max nodes reached", ErrBranchRejected)
	}
	if parent.Depth+1 > state.policy.MaxDepth {
		return fmt.Errorf("%w: max depth reached", ErrBranchRejected)
	}
	if proposal.Topic == "" {
		return fmt.Errorf("%w: topic is required", ErrBranchRejected)
	}
	if !authorizedTarget(proposal.Target, state.target) {
		return fmt.Errorf("%w: target is not authorized", ErrBranchRejected)
	}
	for _, branch := range state.branches {
		if strings.EqualFold(strings.TrimSpace(branch.Topic), strings.TrimSpace(proposal.Topic)) && branch.Scope == proposal.Target && branch.Kind == kind {
			return fmt.Errorf("%w: duplicate branch topic and scope", ErrBranchRejected)
		}
	}
	if kind == BranchHypothesis && parent.Kind == BranchHypothesis && state.childCount(parent.ID) >= state.policy.MaxChildrenPerBranch {
		return fmt.Errorf("%w: child_limit_exceeded", ErrBranchRejected)
	}
	now := time.Now().UTC()
	branch := Branch{
		ID: state.nextID("branch"), ParentID: parent.ID, RunID: state.runID, TaskID: state.task.ID,
		Kind: kind, Depth: parent.Depth + 1, Topic: strings.TrimSpace(proposal.Topic), Goal: proposal.Goal,
		Hypothesis: proposal.Hypothesis, SuccessCondition: proposal.SuccessCondition, FailureCondition: proposal.FailureCondition,
		Scope: proposal.Target, ReviewOf: append([]string(nil), reviewOf...), Status: BranchOpen,
		Priority: proposal.Priority, CreatedAt: now, UpdatedAt: now,
	}
	state.branches = append(state.branches, branch)
	if err := e.persistBranch(branch); err != nil {
		return err
	}
	return e.emit("exploration.branch.created", branch)
}

func (e *explorationEngine) runBatch(ctx context.Context, branches []Branch) ([]attemptExecution, error) {
	state := e.state
	batchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if e.live.backendCompletion() != nil {
		return nil, nil
	}
	executions := make([]attemptExecution, len(branches))
	var wg sync.WaitGroup
	for i, branch := range branches {
		executor := e.executors[i%len(e.executors)]
		sessionID := fmt.Sprintf("executor-%d", i%len(e.executors)+1)
		attempt := Attempt{ID: state.nextID("attempt"), RunID: state.runID, BranchID: branch.ID, SessionID: sessionID, Status: AttemptRunning, StartedAt: time.Now().UTC()}
		executions[i] = attemptExecution{branch: branch, session: executor, sessionID: sessionID, attempt: attempt}
		if index := state.findBranch(branch.ID); index != nil {
			index.Status = BranchActive
			index.UpdatedAt = time.Now().UTC()
			if err := e.persistBranch(*index); err != nil {
				return nil, err
			}
		}
		if err := e.persistAttempt(attempt); err != nil {
			return nil, err
		}
		state.attempts = append(state.attempts, attempt)
		if err := e.emit("exploration.attempt.started", attempt); err != nil {
			return nil, err
		}
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			execution := &executions[index]
			if err := execution.session.Reset(batchCtx); err != nil {
				execution.err = err
				return
			}
			request, dropped := buildExecutorContext(state.task, state.target, execution.branch, state.knowledge, AttemptBudget{MaxAttempts: state.policy.MaxAttemptsPerBranch, MaxToolCalls: state.policy.MaxToolCallsPerAttempt, MaxWallTime: state.policy.MaxWallTimePerAttempt}, e.live.sessionWorkspace(execution.sessionID), state.policy)
			if dropped > 0 {
				if err := e.emit("exploration.context.truncated", map[string]any{"context": "executor", "attempt_id": execution.attempt.ID, "dropped": dropped}); err != nil {
					execution.err = err
					return
				}
			}
			strategy := e.spec.ExecutorStrategy
			if strategy == nil {
				strategy = defaultExecutorStrategy{}
			}
			prompt, err := strategy.Prompt(request)
			if err != nil {
				execution.err = err
				return
			}
			attemptCtx := batchCtx
			cancelAttempt := func() {}
			if request.Budget.MaxWallTime > 0 {
				attemptCtx, cancelAttempt = context.WithTimeout(batchCtx, request.Budget.MaxWallTime)
			}
			defer cancelAttempt()
			turn, err := RunAgentTurn(attemptCtx, execution.session, prompt, TurnLimits{MaxToolCalls: request.Budget.MaxToolCalls, ToolTimeout: DefaultToolCallTimeout}, e.eventSink(execution.sessionID))
			if err != nil {
				execution.err = err
				return
			}
			report, err := strategy.Parse(request, turn)
			if err != nil {
				execution.err = err
				return
			}
			execution.report = report
			e.live.addUsage(turn.Usage)
		}(i)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return executions, nil
	case <-ctx.Done():
		cancel()
		<-done
		return executions, ctx.Err()
	}
}

func (e *explorationEngine) mergeAttempt(ctx context.Context, execution attemptExecution) error {
	state := e.state
	branch := state.findBranch(execution.branch.ID)
	if branch == nil {
		return fmt.Errorf("attempt branch %q disappeared", execution.branch.ID)
	}
	attempt := execution.attempt
	attempt.FinishedAt = time.Now().UTC()
	if execution.err != nil {
		return e.failAttempt(branch, &attempt, execution.err.Error())
	}
	report := execution.report
	attempt.Status = report.Status
	attempt.Outcome = report.Outcome
	attempt.Error = e.live.redactor().Redact(report.Error)
	if attempt.Status == "" {
		attempt.Status = AttemptCompleted
	}
	if attempt.Outcome == "" {
		attempt.Outcome = AttemptInconclusive
	}
	// A blocked or refuted attempt may report no evidence at all: the negative
	// result is itself the finding. Every other outcome must archive evidence.
	needsEvidence := len(report.Observations) > 0 || (attempt.Outcome != AttemptRefuted && attempt.Outcome != AttemptBlocked)
	var observations []Observation
	var err error
	if needsEvidence {
		observations, err = ArchiveAttemptEvidence(ctx, e.spec.Artifacts, *e.live.redactor(), EvidenceArchiveRequest{
			RunID: state.runID, BranchID: branch.ID, AttemptID: attempt.ID, SessionID: execution.sessionID,
			WorkspaceDir: e.live.sessionWorkspace(execution.sessionID), Observations: report.Observations,
			MaxFiles: state.policy.MaxEvidenceFilesPerAttempt, MaxFileBytes: state.policy.MaxArtifactBytes, MaxTotalBytes: state.policy.MaxAttemptArtifactBytes,
		})
		if err != nil {
			var storeErr *ArtifactStoreError
			if errors.As(err, &storeErr) {
				return storeErr
			}
			return e.failAttempt(branch, &attempt, err.Error())
		}
	}
	observationMap := make(map[string]Observation, len(observations))
	for _, observation := range observations {
		state.observations = append(state.observations, observation)
		observationMap[strings.TrimPrefix(observation.ID, attempt.ID+":obs:")] = observation
		attempt.ObservationIDs = append(attempt.ObservationIDs, observation.ID)
		for _, artifact := range observation.Evidence {
			state.artifacts = append(state.artifacts, artifact)
			if err := e.runner.persistArtifact(ctx, e.live.redactor(), artifact); err != nil {
				return err
			}
			if err := e.emit("artifact.stored", artifact); err != nil {
				return err
			}
		}
		if err := e.runner.persistObservation(ctx, e.live.redactor(), observation); err != nil {
			return err
		}
		if err := e.emit("exploration.observation.created", observation); err != nil {
			return err
		}
	}
	reviewResolved := false
	if branch.Kind == BranchReview {
		resolved, err := e.resolveReview(ctx, branch, attempt, report, observationMap)
		if err != nil {
			return err
		}
		reviewResolved = resolved
	} else {
		for _, proposal := range report.Knowledge {
			if len(state.knowledge) >= state.policy.MaxKnowledgeItems {
				if err := e.emit("knowledge.rejected", map[string]any{"attempt_id": attempt.ID, "reason": "knowledge_limit_exceeded"}); err != nil {
					return err
				}
				break
			}
			promoted, events, err := PromoteKnowledge(KnowledgePromotionRequest{RunID: state.runID, AttemptID: attempt.ID, SessionID: execution.sessionID, Task: state.task, Target: state.target, Branch: *branch, Proposal: proposal, Observations: observationMap, Existing: state.knowledge, Now: time.Now().UTC()})
			if err != nil {
				if err := e.emit("knowledge.rejected", map[string]any{"attempt_id": attempt.ID, "error": err.Error()}); err != nil {
					return err
				}
				continue
			}
			state.knowledge = appendOrReplaceKnowledge(state.knowledge, promoted)
			if err := e.runner.persistKnowledge(ctx, e.live.redactor(), promoted); err != nil {
				return err
			}
			for _, event := range events {
				if err := e.emit(event.Type, event.Data); err != nil {
					return err
				}
			}
			for _, conflictID := range promoted.Contradicts {
				if conflictID == promoted.ID {
					continue
				}
				if existing := findKnowledge(state.knowledge, conflictID); existing != nil {
					updated := *existing
					updated.Trust = KnowledgeContested
					updated.LastValidated = time.Now().UTC()
					updated.Contradicts = uniqueSorted(append(updated.Contradicts, promoted.ID))
					updated.Version++
					state.knowledge = appendOrReplaceKnowledge(state.knowledge, updated)
					if err := e.runner.persistKnowledge(ctx, e.live.redactor(), updated); err != nil {
						return err
					}
					if err := e.emit("knowledge.contested", updated); err != nil {
						return err
					}
				}
				if err := e.addReviewBranch(*branch, promoted, conflictID); err != nil {
					if !errors.Is(err, ErrBranchRejected) {
						return err
					}
					if emitErr := e.emit("knowledge.review_rejected", map[string]any{"knowledge_id": promoted.ID, "conflict_id": conflictID, "reason": err.Error()}); emitErr != nil {
						return emitErr
					}
				}
			}
		}
	}
	switch {
	case attempt.Status == AttemptCompleted && attempt.Outcome == AttemptLocalSuccess && len(observations) > 0:
		branch.Status = BranchResolved
		for _, child := range report.ChildBranches {
			if err := e.addBranch(child, *branch, BranchHypothesis, nil); err != nil {
				if emitErr := e.emit("exploration.branch.rejected", map[string]any{"reason": err.Error(), "parent_id": branch.ID}); emitErr != nil {
					return emitErr
				}
			}
		}
	case attempt.Outcome == AttemptRefuted:
		branch.Status = BranchRefuted
	case attempt.Outcome == AttemptBlocked:
		branch.Status = BranchBlocked
	case state.attemptCount(branch.ID) >= state.policy.MaxAttemptsPerBranch:
		branch.Status = BranchBlocked
	default:
		branch.Status = BranchOpen
	}
	if branch.Kind == BranchReview && reviewResolved {
		branch.Status = BranchResolved
	}
	branch.UpdatedAt = time.Now().UTC()
	if err := e.persistAttempt(attempt); err != nil {
		return err
	}
	e.replaceAttempt(attempt)
	if err := e.persistBranch(*branch); err != nil {
		return err
	}
	return e.emit("exploration.attempt.completed", attempt)
}

func (e *explorationEngine) failAttempt(branch *Branch, attempt *Attempt, message string) error {
	attempt.Status = AttemptFailed
	attempt.Outcome = AttemptFailure
	attempt.Error = e.live.redactor().Redact(message)
	if e.state.attemptCount(branch.ID) >= e.state.policy.MaxAttemptsPerBranch {
		branch.Status = BranchBlocked
	} else {
		branch.Status = BranchOpen
	}
	branch.UpdatedAt = time.Now().UTC()
	if err := e.persistAttempt(*attempt); err != nil {
		return err
	}
	e.replaceAttempt(*attempt)
	if err := e.persistBranch(*branch); err != nil {
		return err
	}
	return e.emit("exploration.attempt.failed", *attempt)
}

func (e *explorationEngine) replaceAttempt(attempt Attempt) {
	for i := range e.state.attempts {
		if e.state.attempts[i].ID == attempt.ID {
			e.state.attempts[i] = attempt
			return
		}
	}
}

func appendOrReplaceKnowledge(items []Knowledge, knowledge Knowledge) []Knowledge {
	if existing := findKnowledge(items, knowledge.ID); existing != nil {
		*existing = knowledge
		return items
	}
	return append(items, knowledge)
}

// addReviewBranch opens one review branch per conflicting pair. A review binds
// exactly two knowledge IDs, so multi-way contradictions become several
// independent reviews instead of one oversized binding.
func (e *explorationEngine) addReviewBranch(parent Branch, knowledge Knowledge, conflictID string) error {
	reviewOf := uniqueSorted([]string{knowledge.ID, conflictID})
	if len(reviewOf) != 2 {
		return nil
	}
	for _, branch := range e.state.branches {
		if branch.Kind == BranchReview && equalStringSet(branch.ReviewOf, reviewOf) {
			return nil
		}
	}
	topic := "review." + normalizeKnowledgeTopic(knowledge.Topic) + "." + reviewPairSuffix(reviewOf)
	proposal := BranchProposal{ParentID: parent.ID, Topic: topic, Goal: "Review conflicting knowledge " + strings.Join(reviewOf, " vs "), Hypothesis: "One conflicting claim is supportable", Target: e.state.target.ID, SuccessCondition: "Resolve the bound conflict pair", FailureCondition: "Cannot resolve the conflict"}
	if err := e.addBranch(proposal, parent, BranchReview, reviewOf); err != nil {
		return err
	}
	return e.emit("knowledge.review_required", map[string]any{"review_of": reviewOf})
}

// resolveReview applies one review attempt's verdict to its bound pair. It
// returns true only when the attempt adjudicated both sides of the pair;
// an inconclusive review leaves both items contested for a later attempt.
func (e *explorationEngine) resolveReview(ctx context.Context, branch *Branch, attempt Attempt, report AttemptReport, observations map[string]Observation) (bool, error) {
	winnerID, loserID, err := ResolveReview(branch.ReviewOf, report.Knowledge)
	if err != nil {
		if emitErr := e.emit("knowledge.review_unresolved", map[string]any{"branch_id": branch.ID, "review_of": branch.ReviewOf, "error": err.Error()}); emitErr != nil {
			return false, emitErr
		}
		return false, nil
	}
	winner := findKnowledge(e.state.knowledge, winnerID)
	loser := findKnowledge(e.state.knowledge, loserID)
	if winner == nil || loser == nil {
		if emitErr := e.emit("knowledge.review_unresolved", map[string]any{"branch_id": branch.ID, "review_of": branch.ReviewOf, "error": "bound knowledge is not present"}); emitErr != nil {
			return false, emitErr
		}
		return false, nil
	}
	now := time.Now().UTC()
	updatedWinner := *winner
	updatedWinner.Trust = KnowledgeCorroborated
	updatedWinner.LastValidated = now
	updatedWinner.Sources = appendUniqueSource(updatedWinner.Sources, reviewSources(e.state.runID, attempt, report, winnerID, observations)...)
	updatedWinner.Contradicts = removeString(updatedWinner.Contradicts, loserID)
	updatedWinner.Version++
	updatedLoser := *loser
	updatedLoser.Trust = KnowledgeRefuted
	updatedLoser.LastValidated = now
	updatedLoser.Version++
	e.state.knowledge = appendOrReplaceKnowledge(e.state.knowledge, updatedWinner)
	if err := e.runner.persistKnowledge(ctx, e.live.redactor(), updatedWinner); err != nil {
		return false, err
	}
	e.state.knowledge = appendOrReplaceKnowledge(e.state.knowledge, updatedLoser)
	if err := e.runner.persistKnowledge(ctx, e.live.redactor(), updatedLoser); err != nil {
		return false, err
	}
	if err := e.emit("knowledge.refuted", updatedLoser); err != nil {
		return false, err
	}
	if err := e.emit("knowledge.review_resolved", map[string]any{"branch_id": branch.ID, "winner": updatedWinner.ID, "loser": updatedLoser.ID}); err != nil {
		return false, err
	}
	return true, nil
}

func reviewSources(runID string, attempt Attempt, report AttemptReport, winnerID string, observations map[string]Observation) []KnowledgeSource {
	for _, item := range report.Knowledge {
		if len(item.Supports) != 1 || len(item.Refutes) != 1 || item.Supports[0] != winnerID {
			continue
		}
		sources := make([]KnowledgeSource, 0, len(item.ObservationRefs))
		for _, ref := range item.ObservationRefs {
			if observation, ok := observations[ref]; ok {
				sources = append(sources, KnowledgeSource{RunID: runID, AttemptID: attempt.ID, SessionID: attempt.SessionID, Observation: observation.ID})
			}
		}
		return sources
	}
	return nil
}

func reviewPairSuffix(pair []string) string {
	sum := sha256.Sum256([]byte(strings.Join(pair, "\x00")))
	return hex.EncodeToString(sum[:4])
}

func equalStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]struct{}, len(a))
	for _, value := range a {
		seen[value] = struct{}{}
	}
	for _, value := range b {
		if _, ok := seen[value]; !ok {
			return false
		}
	}
	return true
}

func removeString(values []string, target string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != target {
			out = append(out, value)
		}
	}
	return out
}

func (e *explorationEngine) startSession(ctx context.Context, role AgentRole, spec AgentSpec, sessionID string) (AgentSession, error) {
	if spec.Runtime == nil {
		return nil, fmt.Errorf("%s runtime is required", role)
	}
	workspace := e.live.sessionWorkspace(sessionID)
	if err := ensureDir(workspace); err != nil {
		return nil, err
	}
	env := buildEnvironment(e.state.runID, sessionID, e.spec, spec, e.state.target, workspace, e.live.taskDir, e.live.skillsDir)
	session, err := spec.Runtime.NewSession(ctx, RuntimeRequest{RunID: e.state.runID, SessionID: sessionID, Role: role, Agent: spec, Task: e.state.task, Target: e.state.target, WorkspaceDir: workspace, TaskDir: e.live.taskDir, SkillsDir: e.live.skillsDir, Env: env, Starter: e.runner.starter, Redactor: e.live.redactor()})
	if err != nil {
		return nil, fmt.Errorf("start %s session: %w", role, err)
	}
	e.live.addSession(sessionID, session)
	if logs := session.Logs(); logs != nil {
		go func(sessionID string, logs <-chan LogEvent) {
			for log := range logs {
				_ = e.runner.appendLog(e.live, e.live.redactor(), LogRecord{RunID: e.state.runID, SessionID: sessionID, Stream: log.Stream, Time: time.Now().UTC(), Text: log.Text})
			}
		}(sessionID, logs)
	}
	return session, nil
}

func (e *explorationEngine) closeSession(session AgentSession, sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), e.runner.closeTimeout)
	defer cancel()
	_ = session.Abort(ctx)
	_ = session.Close(ctx)
	e.live.removeSession(sessionID)
}

func (e *explorationEngine) eventSink(sessionID string) EventSink {
	return func(event Event) error {
		event.RunID = e.state.runID
		event.SessionID = sessionID
		return e.runner.emitEvent(e.live, e.live.redactor(), event)
	}
}

func (e *explorationEngine) emit(eventType string, data any) error {
	return e.runner.emitEvent(e.live, e.live.redactor(), Event{RunID: e.state.runID, Source: "harness", Type: eventType, Time: time.Now().UTC(), Data: mustJSON(data)})
}
func (e *explorationEngine) persistBranch(branch Branch) error {
	return e.runner.persistBranch(context.Background(), e.live.redactor(), branch)
}
func (e *explorationEngine) persistAttempt(attempt Attempt) error {
	return e.runner.persistAttempt(context.Background(), e.live.redactor(), attempt)
}

func authorizedTarget(value string, target Target) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.EqualFold(value, target.ID) {
		return true
	}
	valueHost := targetHost(value)
	for _, address := range target.Addresses {
		if strings.EqualFold(value, address) {
			return true
		}
		if valueHost != "" && strings.EqualFold(valueHost, targetHost(address)) {
			return true
		}
	}
	return false
}

func targetHost(value string) string {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "://") {
		if parsed, err := url.Parse(value); err == nil {
			return strings.ToLower(parsed.Hostname())
		}
	}
	if strings.Contains(value, ":") {
		if parsed, err := url.Parse("//" + value); err == nil {
			return strings.ToLower(parsed.Hostname())
		}
	}
	return strings.ToLower(value)
}

func normalizeKnowledgeTopic(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), "."))
}
