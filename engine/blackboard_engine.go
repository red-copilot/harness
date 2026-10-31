package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type activeSlot struct {
	intentID  string
	attemptID string
	cancel    context.CancelFunc
}

type boardAttemptResult struct {
	intent     Intent
	attempt    IntentAttempt
	report     AttemptReport
	conclusion string
	usage      Usage
	err        error
}

type boardReasonResult struct {
	report ReasonReport
	usage  Usage
	err    error
}

type blackboardEngine struct {
	runner     *Runner
	controller *runController
	spec       RunSpec
	target     TargetBinding
	board      *boardState
	committed  boardSnapshot
	planner    AgentSession
	executor   AgentSession
	reason     ReasonStrategy
	explore    ExploreStrategy

	results chan boardAttemptResult
	reasons chan boardReasonResult
	active  *activeSlot

	reasonInFlight bool
	reasonFailures int
	goalCandidate  bool
	stopped        bool
	stagnant       int

	wg         sync.WaitGroup
	sessions   []AgentSession
	sessionIDs []string
}

func newBlackboardEngine(runner *Runner, controller *runController, spec RunSpec, target TargetBinding) *blackboardEngine {
	reason := spec.ReasonStrategy
	if reason == nil {
		reason = defaultReasonStrategy{}
	}
	explore := spec.ExploreStrategy
	if explore == nil {
		explore = defaultExploreStrategy{}
	}
	return &blackboardEngine{
		runner: runner, controller: controller, spec: spec, target: target,
		board: newBoardState(controller.id, spec, target), reason: reason, explore: explore,
		results: make(chan boardAttemptResult, 2), reasons: make(chan boardReasonResult, 1),
	}
}

func validateBlackboardRunSpec(spec RunSpec) error {
	if spec.Executor.Runtime == nil {
		return errors.New("blackboard engine requires one executor")
	}
	if spec.Artifacts == nil {
		return errors.New("blackboard engine requires an artifact store")
	}
	return nil
}

func (e *blackboardEngine) run(ctx context.Context) (runErr error) {
	if err := validateBlackboardRunSpec(e.spec); err != nil {
		return err
	}
	defer func() {
		e.cancelActive()
		e.wg.Wait()
		e.drainBufferedResults()
		runErr = errors.Join(runErr, e.closeSessions())
		if e.controller.controlErr() != nil {
			// Never publish board records that were not persisted.
			e.controller.setBoardSnapshot(e.committed)
		} else {
			e.controller.setBoardSnapshot(e.board.snapshot())
		}
	}()
	if err := e.startSessions(ctx); err != nil {
		return err
	}
	if err := e.persistInitial(); err != nil {
		return err
	}

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if e.controller.backendCompletion() != nil {
			e.drainCanceledAttempts()
			return nil
		}
		if ctx.Err() != nil {
			e.drainCanceledAttempts()
			return ctx.Err()
		}
		if e.goalCandidate && !e.hasActiveSlot() && e.board.openCount() == 0 {
			return nil
		}
		if e.stopped && !e.hasActiveSlot() {
			return nil
		}
		if !e.stopped && !e.goalCandidate {
			e.startOpenIntents(ctx)
			if !e.hasActiveSlot() && e.board.openCount() == 0 && !e.reasonInFlight {
				e.startReason(ctx)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result := <-e.results:
			if err := e.handleAttempt(ctx, result); err != nil {
				return err
			}
		case result := <-e.reasons:
			if err := e.handleReason(ctx, result); err != nil {
				return err
			}
		case <-ticker.C:
		}
	}
}

func (e *blackboardEngine) startSessions(ctx context.Context) error {
	planner, err := e.startSession(ctx, AgentRoleReasoner, e.spec.Reasoner, "planner")
	if err != nil {
		return err
	}
	e.planner = planner
	executor, err := e.startSession(ctx, AgentRoleExecutor, e.spec.Executor, "executor")
	if err != nil {
		return err
	}
	e.executor = executor
	return nil
}

func (e *blackboardEngine) startSession(ctx context.Context, role AgentRole, spec AgentSpec, sessionID string) (AgentSession, error) {
	if spec.Runtime == nil {
		return nil, fmt.Errorf("%s runtime is required", role)
	}
	workspace := e.controller.sessionWorkspace(sessionID)
	if err := ensureDir(workspace); err != nil {
		return nil, err
	}
	env := buildEnvironment(e.controller.id, sessionID, e.spec, spec, e.target, workspace, e.controller.taskDir, e.controller.skillsDir)
	session, err := spec.Runtime.NewSession(ctx, RuntimeRequest{
		RunID: e.controller.id, SessionID: sessionID, Role: role, Agent: spec, Task: e.spec.Task,
		Target: e.target, WorkspaceDir: workspace, TaskDir: e.controller.taskDir, SkillsDir: e.controller.skillsDir,
		Env: env, Starter: e.runner.starter, Redactor: e.controller.redactor(),
	})
	if err != nil {
		return nil, fmt.Errorf("start %s session: %w", role, err)
	}
	e.controller.addSession(sessionID, session)
	e.sessions = append(e.sessions, session)
	e.sessionIDs = append(e.sessionIDs, sessionID)
	if logs := session.Logs(); logs != nil {
		go func(sessionID string, logs <-chan LogEvent) {
			for log := range logs {
				// Log persistence is part of the canonical audit. A failure
				// must stop the Run rather than silently dropping records.
				if err := e.controller.appendLog(LogRecord{RunID: e.controller.id, SessionID: sessionID, Stream: log.Stream, Time: time.Now().UTC(), Text: log.Text}); err != nil {
					e.controller.setControlError(err)
					return
				}
			}
		}(sessionID, logs)
	}
	return session, nil
}

func (e *blackboardEngine) closeSessions() error {
	var errs []error
	for i, session := range e.sessions {
		ctx, cancel := context.WithTimeout(context.Background(), e.runner.closeTimeout)
		abortErr := session.Abort(ctx)
		closeErr := session.Close(ctx)
		cancel()
		status := SessionSucceeded
		if e.controller.ctx.Err() != nil {
			status = SessionCancelled
		} else if abortErr != nil || closeErr != nil {
			status = SessionFailed
		}
		if i < len(e.sessionIDs) {
			e.controller.removeSession(e.sessionIDs[i], status)
		}
		if abortErr != nil {
			errs = append(errs, abortErr)
		}
		if closeErr != nil {
			errs = append(errs, closeErr)
		}
	}
	return errors.Join(errs...)
}

func (e *blackboardEngine) persistInitial() error {
	snapshot := e.board.snapshot()
	events := make([]Event, 0, len(snapshot.Claims))
	for _, claim := range snapshot.Claims {
		event, err := e.controller.canonicalEvent("blackboard", EventClaimCreated, ClaimCreatedPayload{Claim: claim})
		if err != nil {
			return err
		}
		events = append(events, event)
	}
	return e.commit(context.Background(), Commit{
		Revision: snapshot.BoardRevision,
		Claims:   snapshot.Claims,
		Events:   events,
	})
}

func (e *blackboardEngine) eventSink(sessionID string) EventSink {
	return func(event Event) error {
		event.RunID = e.controller.id
		event.SessionID = sessionID
		return e.controller.emitEvent(event)
	}
}

// commit persists one board revision and only advances the committed snapshot
// after the canonical write succeeds. Result projection never includes records
// from a failed commit.
func (e *blackboardEngine) commit(ctx context.Context, commit Commit) error {
	if err := e.controller.commitBoard(ctx, commit); err != nil {
		return err
	}
	e.committed = e.board.snapshot()
	return nil
}

func (e *blackboardEngine) emit(typ EventType, payload any) error {
	event, err := e.controller.canonicalEvent("blackboard", typ, payload)
	if err != nil {
		return err
	}
	return e.controller.emitEvent(event)
}

func (e *blackboardEngine) startOpenIntents(ctx context.Context) {
	if e.hasActiveSlot() {
		return
	}
	for _, intent := range e.board.snapshot().Intents {
		if intent.State != IntentOpen {
			continue
		}
		if err := e.startIntent(ctx, intent); err != nil {
			e.controller.setControlError(err)
			e.stopped = true
		}
		return
	}
}

func (e *blackboardEngine) startIntent(ctx context.Context, intent Intent) error {
	if e.active != nil {
		return fmt.Errorf("%w: executor slot is occupied", ErrIntentCapacity)
	}
	claimed, err := e.board.claimIntent(intent.ID, "executor", time.Now().UTC())
	if err != nil {
		return err
	}
	attemptID := e.board.nextID("attempt")
	attempt := IntentAttempt{ID: attemptID, RunID: e.board.runID, IntentID: intent.ID, SessionID: claimed.ExecutorID, Phase: IntentAttemptRunning, StartedAt: time.Now().UTC()}
	e.board.addAttempt(attempt)
	claimed, err = e.board.registerAttempt(intent.ID, attemptID)
	if err != nil {
		return err
	}
	events := make([]Event, 0, 3)
	if len(intent.AttemptIDs) == 0 {
		createdEvent, err := e.controller.canonicalEvent("blackboard", EventIntentCreated, IntentPayload{Intent: claimed})
		if err != nil {
			return err
		}
		events = append(events, createdEvent)
	}
	claimedEvent, err := e.controller.canonicalEvent("blackboard", EventIntentClaimed, IntentPayload{Intent: claimed})
	if err != nil {
		return err
	}
	startedEvent, err := e.controller.canonicalEvent("blackboard", EventAttemptStarted, IntentAttemptPayload{Attempt: attempt})
	if err != nil {
		return err
	}
	events = append(events, claimedEvent, startedEvent)
	if err := e.commit(ctx, Commit{Revision: e.board.boardRevision(), Intents: []Intent{claimed}, Attempts: []IntentAttempt{attempt}, Events: events}); err != nil {
		return err
	}
	attemptCtx, cancel := context.WithCancel(ctx)
	if wall := e.board.policy.Exploration.MaxWallTimePerAttempt; wall > 0 {
		attemptCtx, cancel = context.WithTimeout(ctx, wall)
	}
	e.active = &activeSlot{intentID: intent.ID, attemptID: attemptID, cancel: cancel}
	requests := e.exploreRequest(claimed)
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		usageBefore := e.executor.Usage()
		if err := e.executor.Reset(attemptCtx); err != nil {
			e.results <- boardAttemptResult{intent: claimed, attempt: attempt, err: err}
			return
		}
		report, conclusion, err := runExploreAttempt(attemptCtx, e.executor, requests, TurnLimits{
			MaxToolCalls:      e.board.policy.Exploration.MaxToolCallsPerAttempt,
			MaxAssistantBytes: e.board.policy.Context.MaxAssistantBytes,
			ToolTimeout:       DefaultToolCallTimeout,
		}, e.board.policy.Exploration.ConcludeTimeout, e.eventSink(claimed.ExecutorID), e.explore)
		usageAfter := e.executor.Usage()
		e.results <- boardAttemptResult{
			intent: claimed, attempt: attempt, report: report,
			conclusion: conclusion, usage: usageDelta(usageBefore, usageAfter), err: err,
		}
	}()
	return nil
}

func (e *blackboardEngine) exploreRequest(intent Intent) ExploreRequest {
	snapshot := e.board.snapshot()
	return ExploreRequest{
		Task: e.board.task, Target: e.board.target, Intent: intent, Claims: boundedClaimsWithPolicy(snapshot.Claims, e.board.policy),
		Budget: AttemptBudget{
			MaxToolCalls: e.board.policy.Exploration.MaxToolCallsPerAttempt,
			MaxWallTime:  e.board.policy.Exploration.MaxWallTimePerAttempt,
		},
		WorkspaceDir: e.controller.sessionWorkspace(intent.ExecutorID), MaxReportBytes: e.board.policy.Evidence.MaxReportBytes,
		BoardRevision: snapshot.BoardRevision,
	}
}

func (e *blackboardEngine) startReason(ctx context.Context) {
	if e.reasonInFlight {
		return
	}
	e.reasonInFlight = true
	snapshot := e.board.snapshot()
	req := buildReasonContext(snapshot, e.board.target, e.board.policy, e.controller.progress())
	dropped := len(snapshot.Claims) - len(req.Claims)
	if dropped > 0 {
		_ = e.emit(EventContextTruncated, ContextTruncatedPayload{Context: "reason", Candidates: len(snapshot.Claims), Kept: len(req.Claims), Dropped: dropped})
	}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		reasonCtx := ctx
		cancel := func() {}
		if e.board.policy.Exploration.MaxReasonerWallTime > 0 {
			reasonCtx, cancel = context.WithTimeout(ctx, e.board.policy.Exploration.MaxReasonerWallTime)
		}
		defer cancel()
		if err := e.emit(EventReasonStarted, ReasonPayload{BoardRevision: snapshot.BoardRevision, CanCreateIntent: true}); err != nil {
			e.reasons <- boardReasonResult{err: err}
			return
		}
		usageBefore := e.planner.Usage()
		if err := e.planner.Reset(reasonCtx); err != nil {
			e.reasons <- boardReasonResult{err: err, usage: usageDelta(usageBefore, e.planner.Usage())}
			return
		}
		prompt, err := e.reason.Prompt(req)
		if err == nil {
			err = clearReport(e.controller.sessionWorkspace("planner"))
		}
		if err == nil {
			var turn TurnResult
			turn, err = runAgentTurn(reasonCtx, e.planner, prompt, TurnLimits{
				MaxToolCalls:      e.board.policy.Exploration.MaxReasonerToolCalls,
				MaxAssistantBytes: e.board.policy.Context.MaxAssistantBytes,
				ToolTimeout:       DefaultToolCallTimeout,
			}, e.eventSink("planner"), true)
			if err == nil {
				turn.Report, err = readReport(e.controller.sessionWorkspace("planner"), e.board.policy.Evidence.MaxReportBytes)
			}
			if err == nil {
				var report ReasonReport
				report, err = e.reason.Parse(req, turn)
				e.reasons <- boardReasonResult{report: report, usage: usageDelta(usageBefore, e.planner.Usage()), err: err}
				return
			}
		}
		e.reasons <- boardReasonResult{usage: usageDelta(usageBefore, e.planner.Usage()), err: err}
	}()
}

func (e *blackboardEngine) handleReason(ctx context.Context, result boardReasonResult) error {
	e.reasonInFlight = false
	if err := e.controller.addUsage(result.usage); err != nil {
		return err
	}
	if result.err != nil {
		e.reasonFailures++
		_ = e.emit(EventReasonRejected, e.reasonPayload(result.report, e.controller.redactor().Redact(result.err.Error())))
		if e.reasonFailures >= e.board.policy.Exploration.MaxReasonerFailures {
			e.stopped = true
			e.controller.setStagnated()
		}
		return nil
	}
	if result.report.GoalCandidate != nil {
		e.reasonFailures = 0
		e.goalCandidate = true
		return e.emit(EventReasonCompleted, e.reasonPayload(result.report, ""))
	}
	if len(result.report.Intents) == 0 {
		e.stopped = true
		e.controller.setStagnated()
		return e.emit(EventReasonCompleted, e.reasonPayload(result.report, ""))
	}
	intents, err := admitReasonIntents(e.board, result.report, true)
	if err != nil {
		e.reasonFailures++
		_ = e.emit(EventIntentRejected, IntentRejectedPayload{
			BoardRevision: result.report.BoardRevision,
			Intents:       append([]IntentProposal(nil), result.report.Intents...),
			GoalCandidate: result.report.GoalCandidate,
			Reason:        e.controller.redactor().Redact(err.Error()),
		})
		if e.reasonFailures >= e.board.policy.Exploration.MaxReasonerFailures {
			e.stopped = true
			e.controller.setStagnated()
		}
		return nil
	}
	e.reasonFailures = 0
	if len(intents) == 0 {
		e.stopped = true
		e.controller.setStagnated()
		return nil
	}
	for _, intent := range intents {
		if err := e.startIntent(ctx, intent); err != nil {
			return err
		}
	}
	return e.emit(EventReasonCompleted, e.reasonPayload(result.report, ""))
}

func (e *blackboardEngine) reasonPayload(report ReasonReport, errText string) ReasonPayload {
	return ReasonPayload{
		BoardRevision:   report.BoardRevision,
		CanCreateIntent: true,
		Intents:         append([]IntentProposal(nil), report.Intents...),
		GoalCandidate:   report.GoalCandidate,
		Error:           errText,
	}
}

func (e *blackboardEngine) retryOrAbandon(intent Intent, reason string) (Intent, EventType) {
	attempts := len(intent.AttemptIDs)
	if current := e.board.findIntent(intent.ID); current != nil {
		attempts = len(current.AttemptIDs)
	}
	if attempts >= e.board.policy.Exploration.MaxAttemptsPerIntent {
		updated, err := e.board.abandonIntent(intent.ID, reason, time.Now().UTC())
		if err == nil {
			return updated, EventIntentAbandoned
		}
		return intent, ""
	}
	updated, err := e.board.returnIntentToOpen(intent.ID, time.Now().UTC())
	if err == nil {
		return updated, EventIntentReleased
	}
	return intent, ""
}

func (e *blackboardEngine) handleAttempt(ctx context.Context, result boardAttemptResult) error {
	if e.active != nil {
		e.active.cancel()
		e.active = nil
	}
	if err := e.controller.addUsage(result.usage); err != nil {
		return err
	}
	result.attempt.FinishedAt = time.Now().UTC()
	result.attempt.Conclusion = result.conclusion

	if result.err != nil {
		result.attempt.Phase = IntentAttemptFailed
		result.attempt.Error = e.controller.redactor().Redact(result.err.Error())
		e.board.updateAttempt(result.attempt)
		updated, intentEventType := e.retryOrAbandon(result.intent, result.attempt.Error)
		e.stagnant++
		events, err := e.attemptEvents(result.attempt, updated, EventAttemptFailed, intentEventType)
		if err != nil {
			return err
		}
		if err := e.commit(ctx, Commit{Revision: e.board.boardRevision(), Intents: []Intent{updated}, Attempts: []IntentAttempt{result.attempt}, Events: events}); err != nil {
			return err
		}
		return e.finishAttemptBookkeeping()
	}

	var claim Claim
	var observations []Observation
	var artifacts []Artifact
	haveClaim := false
	if result.report.Claim != nil {
		if result.report.Claim.Kind == ClaimFinding || result.report.Claim.Kind == ClaimNegative {
			selected := referencedObservations(result.report)
			collected, collectedArtifacts, err := e.collectEvidence(ctx, result, selected)
			if err != nil {
				result.attempt.Phase = IntentAttemptFailed
				result.attempt.Error = e.controller.redactor().Redact(err.Error())
				e.board.updateAttempt(result.attempt)
				var artifactStoreErr *ArtifactStoreError
				if errors.As(err, &artifactStoreErr) {
					abandoned, abandonErr := e.board.abandonIntent(result.intent.ID, "artifact store failure: "+result.attempt.Error, time.Now().UTC())
					if abandonErr != nil {
						return errors.Join(err, abandonErr)
					}
					events, eventErr := e.attemptEvents(result.attempt, abandoned, EventAttemptFailed, EventIntentAbandoned)
					if eventErr != nil {
						return errors.Join(err, eventErr)
					}
					if commitErr := e.commit(ctx, Commit{Revision: e.board.boardRevision(), Intents: []Intent{abandoned}, Attempts: []IntentAttempt{result.attempt}, Events: events}); commitErr != nil {
						return errors.Join(err, commitErr)
					}
					return err
				}
				updated, intentEventType := e.retryOrAbandon(result.intent, result.attempt.Error)
				events, eventErr := e.attemptEvents(result.attempt, updated, EventAttemptFailed, intentEventType)
				if eventErr != nil {
					return eventErr
				}
				if commitErr := e.commit(ctx, Commit{Revision: e.board.boardRevision(), Intents: []Intent{updated}, Attempts: []IntentAttempt{result.attempt}, Events: events}); commitErr != nil {
					return commitErr
				}
				return e.finishAttemptBookkeeping()
			}
			observations = collected
			artifacts = collectedArtifacts
			for _, observation := range observations {
				e.board.addObservation(observation)
			}
			for _, artifact := range artifacts {
				e.board.addArtifact(artifact)
			}
			observationIDs := make([]string, 0, len(observations))
			artifactIDs := make([]string, 0, len(artifacts))
			for _, observation := range observations {
				observationIDs = append(observationIDs, observation.ID)
			}
			for _, artifact := range artifacts {
				artifactIDs = append(artifactIDs, artifact.ID)
			}
			claim, err = e.board.addClaim(Claim{Kind: result.report.Claim.Kind, Description: result.report.Claim.Description, SourceIntentID: result.intent.ID, ObservationIDs: observationIDs, ArtifactIDs: artifactIDs})
			if err != nil {
				return err
			}
			haveClaim = true
			if result.report.Claim.Kind == ClaimNegative {
				e.stagnant++
			} else {
				e.stagnant = 0
			}
		}
	}

	claims := []Claim(nil)
	if haveClaim {
		claims = []Claim{claim}
	}
	updated, intentEventType := e.board.findIntent(result.intent.ID), EventType("")
	attemptEventType := EventAttemptConcluded
	if result.report.Claim != nil {
		concluded, err := e.board.concludeIntent(result.intent.ID, claim.ID, time.Now().UTC())
		if err != nil {
			return err
		}
		updated = &concluded
		result.attempt.Phase = IntentAttemptConcluded
		intentEventType = EventIntentConcluded
	} else {
		abandoned, _ := e.board.abandonIntent(result.intent.ID, "no claim produced", time.Now().UTC())
		updated = &abandoned
		result.attempt.Phase = IntentAttemptFailed
		attemptEventType = EventAttemptFailed
		intentEventType = EventIntentAbandoned
		e.stagnant++
	}
	e.board.updateAttempt(result.attempt)
	events, err := e.attemptEvents(result.attempt, *updated, attemptEventType, intentEventType)
	if err != nil {
		return err
	}
	if haveClaim {
		claimEvent, eventErr := e.controller.canonicalEvent("blackboard", EventClaimCreated, ClaimCreatedPayload{Claim: claim})
		if eventErr != nil {
			return eventErr
		}
		events = append(events, claimEvent)
	}
	for _, observation := range observations {
		event, eventErr := e.controller.canonicalEvent("blackboard", EventObservationCreated, ObservationCreatedPayload{Observation: observation})
		if eventErr != nil {
			return eventErr
		}
		events = append(events, event)
	}
	for _, artifact := range artifacts {
		event, eventErr := e.controller.canonicalEvent("blackboard", EventArtifactStored, ArtifactStoredPayload{Artifact: artifact})
		if eventErr != nil {
			return eventErr
		}
		events = append(events, event)
	}
	if err := e.commit(ctx, Commit{Revision: e.board.boardRevision(), Claims: claims, Intents: []Intent{*updated}, Attempts: []IntentAttempt{result.attempt}, Observations: observations, Artifacts: artifacts, Events: events}); err != nil {
		return err
	}
	return e.finishAttemptBookkeeping()
}

func (e *blackboardEngine) attemptEvents(attempt IntentAttempt, intent Intent, attemptType EventType, intentType EventType) ([]Event, error) {
	attemptEvent, err := e.controller.canonicalEvent("blackboard", attemptType, IntentAttemptPayload{Attempt: attempt})
	if err != nil {
		return nil, err
	}
	events := []Event{attemptEvent}
	if intentType == "" {
		return events, nil
	}
	intentEvent, err := e.controller.canonicalEvent("blackboard", intentType, IntentPayload{Intent: intent})
	if err != nil {
		return nil, err
	}
	return append(events, intentEvent), nil
}

func (e *blackboardEngine) finishAttemptBookkeeping() error {
	if e.stagnant >= e.board.policy.Exploration.StagnationRounds {
		e.stopped = true
		e.controller.setStagnated()
	}
	return nil
}

func referencedObservations(report AttemptReport) []ObservationProposal {
	if report.Claim == nil || len(report.Claim.ObservationRefs) == 0 {
		return nil
	}
	byID := make(map[string]ObservationProposal, len(report.Observations))
	for _, observation := range report.Observations {
		byID[observation.LocalID] = observation
	}
	selected := make([]ObservationProposal, 0, len(report.Claim.ObservationRefs))
	for _, ref := range report.Claim.ObservationRefs {
		if observation, ok := byID[ref]; ok {
			selected = append(selected, observation)
		}
	}
	return selected
}

func (e *blackboardEngine) collectEvidence(ctx context.Context, result boardAttemptResult, proposals []ObservationProposal) ([]Observation, []Artifact, error) {
	observations, err := ArchiveAttemptEvidence(ctx, e.spec.Artifacts, *e.controller.redactor(), EvidenceArchiveRequest{
		RunID: result.attempt.RunID, IntentID: result.intent.ID, AttemptID: result.attempt.ID,
		SessionID: result.attempt.SessionID, WorkspaceDir: e.controller.sessionWorkspace(result.attempt.SessionID), Target: e.board.target,
		Observations: proposals, MaxFiles: e.board.policy.Evidence.MaxEvidenceFilesPerAttempt,
		MaxFileBytes: e.board.policy.Evidence.MaxArtifactBytes, MaxTotalBytes: e.board.policy.Evidence.MaxAttemptArtifactBytes,
	})
	if err != nil {
		return nil, nil, err
	}
	var artifacts []Artifact
	for _, observation := range observations {
		artifacts = append(artifacts, observation.Evidence...)
	}
	return observations, artifacts, nil
}

func (e *blackboardEngine) hasActiveSlot() bool { return e.active != nil }

func (e *blackboardEngine) cancelActive() {
	if e.active != nil && e.active.cancel != nil {
		e.active.cancel()
	}
}

func (e *blackboardEngine) drainCanceledAttempts() {
	e.cancelActive()
	deadline := time.After(3 * time.Second)
	for e.hasActiveSlot() {
		select {
		case result := <-e.results:
			e.recordCanceledAttempt(result)
		case <-deadline:
			return
		}
	}
	if e.reasonInFlight {
		select {
		case result := <-e.reasons:
			e.reasonInFlight = false
			_ = e.controller.addUsage(result.usage)
			if result.err != nil {
				_ = e.emit(EventReasonRejected, e.reasonPayload(result.report, result.err.Error()))
			}
		default:
		}
	}
}

func (e *blackboardEngine) drainBufferedResults() {
	for {
		select {
		case result := <-e.results:
			e.recordCanceledAttempt(result)
		default:
			goto reasons
		}
	}
reasons:
	for {
		select {
		case result := <-e.reasons:
			e.reasonInFlight = false
			_ = e.controller.addUsage(result.usage)
		default:
			return
		}
	}
}

func (e *blackboardEngine) recordCanceledAttempt(result boardAttemptResult) {
	if e.active != nil {
		e.active.cancel()
		e.active = nil
	}
	_ = e.controller.addUsage(result.usage)
	result.attempt.FinishedAt = time.Now().UTC()
	result.attempt.Phase = IntentAttemptCancelled
	result.attempt.Conclusion = result.conclusion
	if result.err != nil {
		result.attempt.Error = e.controller.redactor().Redact(result.err.Error())
	}
	e.board.updateAttempt(result.attempt)
	var intents []Intent
	var intentEventType EventType
	if intent := e.board.findIntent(result.intent.ID); intent != nil && intent.State == IntentClaimed {
		abandoned, _ := e.board.abandonIntent(intent.ID, "run canceled before conclusion", time.Now().UTC())
		intents = append(intents, abandoned)
		intentEventType = EventIntentAbandoned
	}
	var intent Intent
	if len(intents) > 0 {
		intent = intents[0]
	}
	events, err := e.attemptEvents(result.attempt, intent, EventAttemptFailed, intentEventType)
	if err == nil {
		_ = e.commit(context.Background(), Commit{Revision: e.board.boardRevision(), Intents: intents, Attempts: []IntentAttempt{result.attempt}, Events: events})
	}
}

func usageDelta(before, after Usage) Usage {
	delta := Usage{
		InputTokens:  after.InputTokens - before.InputTokens,
		OutputTokens: after.OutputTokens - before.OutputTokens,
		TotalTokens:  after.TotalTokens - before.TotalTokens,
		ModelCalls:   after.ModelCalls - before.ModelCalls,
		ToolCalls:    after.ToolCalls - before.ToolCalls,
		CostUSD:      after.CostUSD - before.CostUSD,
	}
	if delta.InputTokens < 0 {
		delta.InputTokens = 0
	}
	if delta.OutputTokens < 0 {
		delta.OutputTokens = 0
	}
	if delta.TotalTokens < 0 {
		delta.TotalTokens = 0
	}
	if delta.ModelCalls < 0 {
		delta.ModelCalls = 0
	}
	if delta.ToolCalls < 0 {
		delta.ToolCalls = 0
	}
	if delta.CostUSD < 0 {
		delta.CostUSD = 0
	}
	return delta
}
