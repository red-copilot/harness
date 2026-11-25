package kernel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/red-copilot/harness/core"
	"github.com/red-copilot/harness/engine"
)

// activeRun is the single controller for one Run. Only this goroutine advances
// the lifecycle; turn goroutines deliver results and persist agent events
// through the serialized writer.
type activeRun struct {
	runner   *Runner
	id       string
	spec     Spec
	manifest core.Manifest
	digest   string
	ctx      context.Context
	cancel   context.CancelFunc
	writer   *writer
	events   chan core.Record
	done     chan struct{}

	root        string
	dirs        engine.RunDirs
	target      core.TargetBinding
	targetOwned bool

	sessions   map[string]engine.AgentSession
	sessionIDs []string
	planner    engine.AgentSession
	executor   engine.AgentSession

	resultCh chan attemptResult
	reasonCh chan reasonResult

	active         *activeSlot
	reasonInFlight bool
	stopped        bool
	goalCandidate  bool
	stagnated      bool
	nonGrowth      int
	reasonFailures int
	resumed        bool

	errMu      sync.Mutex
	controlErr error
	budgetErr  error

	resultMu  sync.RWMutex
	result    core.RunResult
	resultErr error
}

func (r *activeRun) policy() core.RunPolicy { return r.manifest.Policy }

func (r *activeRun) now() time.Time {
	if r.runner.clock == nil {
		return time.Time{}
	}
	return r.runner.clock.Now().UTC()
}

func (r *activeRun) drive() {
	defer close(r.done)
	if err := runSafely(r.lifecycle); err != nil {
		r.setControlError(err)
	}
	if err := runSafely(r.finish); err != nil {
		r.setResult(core.RunResult{RunID: r.id, Phase: r.writer.stateSnapshot().Phase, Error: err.Error()}, err)
	}
}

func (r *activeRun) lifecycle() error {
	if err := r.prepareInitial(); err != nil {
		return err
	}
	if r.writer.stateSnapshot().Phase == core.PhaseVerifying {
		return nil
	}
	if err := r.startSessions(); err != nil {
		return err
	}
	return r.loop()
}

func (r *activeRun) prepareInitial() error {
	if err := r.prepareWorkspace(); err != nil {
		return err
	}
	state := r.writer.stateSnapshot()
	if r.resumed {
		if _, err := r.writer.resume(r.ctx, r.now()); err != nil {
			return err
		}
		state = r.writer.stateSnapshot()
		r.goalCandidate = state.GoalCandidate
	}
	switch state.Phase {
	case core.PhaseQueued:
		return r.startFresh()
	case core.PhaseProvisioning:
		if err := r.attachTarget(r.ctx); err != nil {
			return err
		}
		entries := []entry{{core.RecordTargetProvisioned, core.TargetProvisionedRecord{Binding: r.target}}}
		if !hasClaim(r.writer.boardSnapshot(), "goal") {
			entries = append(entries, r.goalClaim(r.now()))
		}
		_, err := r.writer.advance(r.ctx, core.Transition{Kind: core.TransitionProvisioned, At: r.now()}, entries...)
		return err
	case core.PhaseRunning:
		if err := r.attachTarget(r.ctx); err != nil {
			return err
		}
		_, err := r.writer.append(r.ctx, r.now(), entry{core.RecordTargetProvisioned, core.TargetProvisionedRecord{Binding: r.target}})
		return err
	case core.PhaseVerifying:
		if err := r.attachTarget(r.ctx); err != nil {
			return err
		}
		_, err := r.writer.append(r.ctx, r.now(), entry{core.RecordTargetProvisioned, core.TargetProvisionedRecord{Binding: r.target}})
		return err
	default:
		return fmt.Errorf("cannot resume from phase %q", state.Phase)
	}
}

func (r *activeRun) startFresh() error {
	at := r.now()
	if _, err := r.writer.advance(r.ctx, core.Transition{Kind: core.TransitionStart, At: at}, r.originClaim(at)); err != nil {
		return err
	}
	if err := r.attachTarget(r.ctx); err != nil {
		return err
	}
	at = r.now()
	_, err := r.writer.advance(r.ctx, core.Transition{Kind: core.TransitionProvisioned, At: at},
		entry{core.RecordTargetProvisioned, core.TargetProvisionedRecord{Binding: r.target}},
		r.goalClaim(at),
	)
	return err
}

func (r *activeRun) prepareWorkspace() error {
	dirs, err := r.runner.workspace.PrepareRun(r.ctx, r.root, r.id, r.spec.MaterializeSkills)
	if err != nil {
		return fmt.Errorf("prepare workspace: %w", err)
	}
	r.dirs = dirs
	return nil
}

func (r *activeRun) originClaim(at time.Time) entry {
	return entry{core.RecordClaimCreated, core.ClaimCreatedRecord{Claim: core.Claim{
		ID: "origin", RunID: r.id, TaskID: r.manifest.Task.ID, Kind: core.ClaimOrigin,
		Description: r.manifest.Task.Objective, CreatedAt: at,
	}}}
}

func (r *activeRun) goalClaim(at time.Time) entry {
	return entry{core.RecordClaimCreated, core.ClaimCreatedRecord{Claim: core.Claim{
		ID: "goal", RunID: r.id, TaskID: r.manifest.Task.ID, Kind: core.ClaimGoal,
		Description: r.manifest.Task.Objective, CreatedAt: at,
	}}}
}

func hasClaim(board core.Board, id string) bool {
	for _, claim := range board.Claims {
		if claim.ID == id {
			return true
		}
	}
	return false
}

func (r *activeRun) attachTarget(ctx context.Context) error {
	binding, err := r.provision(ctx)
	if err != nil {
		return err
	}
	r.target = binding
	r.targetOwned = r.spec.Provisioner != nil
	return nil
}

func (r *activeRun) provision(ctx context.Context) (core.TargetBinding, error) {
	spec := r.manifest.Target
	if r.spec.Provisioner == nil {
		return core.TargetBinding{
			TargetID: core.TargetFingerprint(spec), Addresses: append([]string(nil), spec.Addresses...),
			Isolation: spec.Isolation,
		}, nil
	}
	binding, err := r.spec.Provisioner.Provision(ctx, core.ProvisionRequest{RunID: r.id, Task: r.manifest.Task, Target: spec})
	if err != nil {
		return core.TargetBinding{}, fmt.Errorf("provision target: %w", err)
	}
	if binding.TargetID == "" {
		binding.TargetID = core.TargetFingerprint(spec)
	}
	if len(binding.Addresses) == 0 {
		binding.Addresses = append([]string(nil), spec.Addresses...)
	}
	if binding.Isolation == "" {
		binding.Isolation = spec.Isolation
	}
	if err := core.ValidateTargetBinding(spec, binding); err != nil {
		return core.TargetBinding{}, err
	}
	return binding, nil
}

func (r *activeRun) startSessions() error {
	planner, err := r.startSession("planner", core.AgentRoleReasoner, r.spec.Reasoner)
	if err != nil {
		return err
	}
	r.planner = planner
	executor, err := r.startSession("executor", core.AgentRoleExecutor, r.spec.Executor)
	if err != nil {
		return err
	}
	r.executor = executor
	return nil
}

func (r *activeRun) startSession(sessionID string, role core.AgentRole, agent engine.AgentSpec) (engine.AgentSession, error) {
	if agent.Runtime == nil {
		return nil, fmt.Errorf("%s runtime is required", role)
	}
	if r.dirs == nil {
		return nil, errors.New("run workspace is not prepared")
	}
	workspaceDir, err := r.dirs.PrepareSession(r.ctx, sessionID)
	if err != nil {
		return nil, err
	}
	env := buildEnvironment(r.id, sessionID, r.spec, agent, r.target, workspaceDir, r.dirs.TaskDir(), r.dirs.SkillsDir(), r.runner.secrets)
	session, err := agent.Runtime.NewSession(r.ctx, engine.RuntimeRequest{
		RunID: r.id, SessionID: sessionID, Role: role, Agent: agent, Task: r.manifest.Task,
		Target: r.target, WorkspaceDir: workspaceDir, TaskDir: r.dirs.TaskDir(), SkillsDir: r.dirs.SkillsDir(),
		Env: env, Redactor: r.writer.redactor,
	})
	if err != nil {
		return nil, fmt.Errorf("start %s session: %w", role, err)
	}
	r.sessions[sessionID] = session
	r.sessionIDs = append(r.sessionIDs, sessionID)
	if logs := session.Logs(); logs != nil {
		go r.pumpLogs(sessionID, logs)
	}
	return session, nil
}

func (r *activeRun) pumpLogs(sessionID string, logs <-chan core.LogEvent) {
	_ = runSafely(func() error {
		for log := range logs {
			if r.ctx.Err() != nil {
				return nil
			}
			if err := r.writer.appendLog(r.ctx, core.LogRecord{
				RunID: r.id, SessionID: sessionID, Stream: log.Stream, Time: r.now(), Text: log.Text,
			}); err != nil {
				if r.ctx.Err() == nil && !errors.Is(err, core.ErrTerminal) && !errors.Is(err, ErrRunClosed) {
					r.setControlError(err)
				}
				return nil
			}
		}
		return nil
	})
}

func (r *activeRun) loop() error {
	interval := r.policy().Exploration.EnginePollInterval
	if interval <= 0 {
		interval = core.DefaultRunPolicy().Exploration.EnginePollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if r.ctx.Err() != nil {
			return nil
		}
		if r.active == nil && (r.stopped || r.goalCandidate) {
			return nil
		}
		if r.active == nil && !r.stopped && !r.goalCandidate {
			if intent, ok := r.openIntent(); ok {
				if err := r.startAttempt(intent); err != nil {
					return err
				}
			} else if !r.reasonInFlight {
				if err := r.startReason(); err != nil {
					return err
				}
			}
		}
		select {
		case <-r.ctx.Done():
			return nil
		case result := <-r.resultCh:
			if err := r.handleAttempt(result); err != nil {
				return err
			}
		case result := <-r.reasonCh:
			if err := r.handleReason(result); err != nil {
				return err
			}
		case <-ticker.C:
		}
	}
}

func (r *activeRun) openIntent() (core.Intent, bool) {
	for _, intent := range r.writer.boardSnapshot().Intents {
		if intent.State == core.IntentOpen {
			return intent, true
		}
	}
	return core.Intent{}, false
}

func (r *activeRun) startAttempt(intent core.Intent) error {
	now := r.now()
	claimed := intent
	claimed.State = core.IntentClaimed
	claimed.ExecutorID = "executor"
	claimed.ClaimedAt = &now
	attemptID, err := r.writer.entity("attempt")
	if err != nil {
		return err
	}
	attempt := core.IntentAttempt{
		ID: attemptID, RunID: r.id, IntentID: intent.ID, SessionID: "executor",
		Phase: core.IntentAttemptRunning, StartedAt: now,
	}
	if _, err := r.writer.append(r.ctx, now,
		entry{core.RecordIntentClaimed, core.IntentClaimedRecord{IntentID: intent.ID, ExecutorID: "executor", At: now}},
		entry{core.RecordAttemptStarted, core.AttemptStartedRecord{Attempt: attempt}},
	); err != nil {
		return err
	}
	attemptCtx, cancel := childContext(r.ctx, r.policy().Exploration.MaxWallTimePerAttempt)
	slot := &activeSlot{cancel: cancel, intent: claimed, attempt: attempt}
	r.active = slot
	go func() {
		usageBefore := r.executor.Usage()
		var report core.AttemptReport
		var conclusion string
		var runErr error
		func() {
			defer capturePanic(&runErr)
			if err := r.executor.Reset(attemptCtx); err != nil {
				runErr = err
				return
			}
			report, conclusion, runErr = engine.RunExploreAttempt(
				attemptCtx, r.executor, r.exploreRequest(claimed),
				core.TurnLimits{
					MaxToolCalls:      r.policy().Exploration.MaxToolCallsPerAttempt,
					MaxAssistantBytes: r.policy().Context.MaxAssistantBytes,
				},
				r.policy().Exploration.ConcludeTimeout, r.eventSink(attemptCtx),
				r.exploreStrategy(), r.runner.clock, r.runner.workspace,
			)
		}()
		usage := usageDelta(usageBefore, r.executor.Usage())
		r.deliverAttempt(attemptCtx, attemptResult{
			intent: claimed, attempt: attempt, report: report,
			conclusion: conclusion, usage: usage, err: runErr,
		})
	}()
	return nil
}

func (r *activeRun) deliverAttempt(ctx context.Context, result attemptResult) {
	select {
	case r.resultCh <- result:
	case <-ctx.Done():
		select {
		case r.resultCh <- result:
		default:
		}
	}
}

func (r *activeRun) startReason() error {
	if r.reasonInFlight {
		return nil
	}
	r.reasonInFlight = true
	board := r.writer.boardSnapshot()
	revision := board.Revision
	request := r.reasonRequest(board)
	if _, err := r.writer.append(r.ctx, r.now(), entry{core.RecordReasonStarted, core.ReasonStartedRecord{
		BoardRevision: revision, CanCreateIntent: true,
	}}); err != nil {
		r.reasonInFlight = false
		return err
	}
	go func() {
		reasonCtx, cancel := childContext(r.ctx, r.policy().Exploration.MaxReasonerWallTime)
		defer cancel()
		usageBefore := r.planner.Usage()
		var report core.ReasonReport
		var runErr error
		func() {
			defer capturePanic(&runErr)
			if err := r.planner.Reset(reasonCtx); err != nil {
				runErr = err
				return
			}
			prompt, err := r.reasonStrategy().Prompt(request)
			if err != nil {
				runErr = err
				return
			}
			if err := r.runner.workspace.ClearReport(reasonCtx, r.dirs.SessionDir("planner")); err != nil {
				runErr = err
				return
			}
			turn, err := engine.RunAgentTurn(reasonCtx, r.planner, prompt, core.TurnLimits{
				MaxToolCalls:      r.policy().Exploration.MaxReasonerToolCalls,
				MaxAssistantBytes: r.policy().Context.MaxAssistantBytes,
			}, r.eventSink(reasonCtx), r.runner.clock)
			if err != nil {
				runErr = err
				return
			}
			turn.Report, runErr = r.runner.workspace.ReadReport(reasonCtx, r.dirs.SessionDir("planner"), r.policy().Evidence.MaxReportBytes)
			if runErr != nil {
				return
			}
			report, runErr = r.reasonStrategy().Parse(request, turn)
		}()
		usage := usageDelta(usageBefore, r.planner.Usage())
		result := reasonResult{report: report, boardRevision: revision, usage: usage, err: runErr}
		select {
		case r.reasonCh <- result:
		case <-reasonCtx.Done():
			select {
			case r.reasonCh <- result:
			default:
			}
		}
	}()
	return nil
}

func (r *activeRun) handleReason(result reasonResult) error {
	r.reasonInFlight = false
	if err := r.addUsage(r.ctx, result.usage); err != nil {
		return err
	}
	if result.err != nil {
		return r.rejectReason(result, result.err)
	}
	board := r.writer.boardSnapshot()
	if result.report.BoardRevision != 0 && result.report.BoardRevision != result.boardRevision {
		return r.rejectReason(result, fmt.Errorf("stale reason board revision %d, want %d", result.report.BoardRevision, result.boardRevision))
	}
	if result.report.GoalCandidate != nil {
		r.reasonFailures = 0
		r.goalCandidate = true
		_, err := r.writer.append(r.ctx, r.now(), entry{core.RecordReasonCompleted, core.ReasonCompletedRecord{GoalCandidate: true}})
		return err
	}
	if len(result.report.Intents) == 0 {
		if _, err := r.writer.append(r.ctx, r.now(), entry{core.RecordReasonCompleted, core.ReasonCompletedRecord{}}); err != nil {
			return err
		}
		return r.stagnate("reason produced no intents while the executor slot was free")
	}
	if err := core.ValidateReasonReport(result.report, true); err != nil {
		return r.rejectReason(result, err)
	}
	if max := r.policy().Exploration.MaxTotalIntents; max > 0 && len(board.Intents) >= max {
		return fmt.Errorf("max total intents reached: %d", max)
	}
	now := r.now()
	intentID, err := r.writer.entity("intent")
	if err != nil {
		return err
	}
	proposal := result.report.Intents[0]
	intent := core.Intent{
		ID: intentID, RunID: r.id, FromClaimIDs: append([]string(nil), proposal.From...),
		Description: proposal.Description, State: core.IntentOpen, CreatedAt: now,
	}
	if err := core.ValidateIntent(intent); err != nil {
		return r.rejectReason(result, err)
	}
	for _, claimID := range intent.FromClaimIDs {
		if !boardHasClaim(board, claimID) {
			return r.rejectReason(result, fmt.Errorf("intent source claim %q does not exist", claimID))
		}
	}
	r.reasonFailures = 0
	_, err = r.writer.append(r.ctx, now,
		entry{core.RecordIntentProposed, core.IntentProposedRecord{Intent: intent}},
		entry{core.RecordReasonCompleted, core.ReasonCompletedRecord{IntentIDs: []string{intent.ID}}},
	)
	return err
}

func boardHasClaim(board core.Board, id string) bool { return hasClaim(board, id) }

func (r *activeRun) rejectReason(result reasonResult, err error) error {
	r.reasonFailures++
	redacted := r.writer.redactor.Redact(err.Error())
	if _, appendErr := r.writer.append(r.ctx, r.now(), entry{core.RecordReasonRejected, core.ReasonRejectedRecord{Error: redacted, At: r.now()}}); appendErr != nil {
		return appendErr
	}
	if max := r.policy().Exploration.MaxReasonerFailures; max > 0 && r.reasonFailures >= max {
		return r.stagnate("reasoner failures reached the configured maximum")
	}
	return nil
}

func (r *activeRun) handleAttempt(result attemptResult) error {
	if r.active != nil {
		r.active.cancel()
		r.active = nil
	}
	if err := r.addUsage(r.ctx, result.usage); err != nil {
		return err
	}
	result.attempt.FinishedAt = r.now()
	result.attempt.Conclusion = result.conclusion
	if result.err != nil {
		return r.failAttempt(result, r.writer.redactor.Redact(result.err.Error()), false)
	}
	if result.report.Claim == nil {
		return r.failAttempt(result, "attempt produced no claim", false)
	}
	now := r.now()
	claimProposal := result.report.Claim
	if !claimProposal.Kind.RequiresEvidence() {
		return r.failAttempt(result, "attempt produced an unsupported claim kind", false)
	}
	selected := referencedObservations(result.report)
	observations, err := archiveEvidence(r.ctx, r.runner.store, r.writer.redactor, r.runner.workspace, core.EvidenceArchiveRequest{
		Now: now, RunID: r.id, IntentID: result.intent.ID, AttemptID: result.attempt.ID,
		SessionID: result.attempt.SessionID, WorkspaceDir: r.dirs.SessionDir(result.attempt.SessionID),
		Target: r.target, Observations: selected,
		MaxFiles:     r.policy().Evidence.MaxEvidenceFilesPerAttempt,
		MaxFileBytes: r.policy().Evidence.MaxArtifactBytes, MaxTotalBytes: r.policy().Evidence.MaxAttemptArtifactBytes,
	})
	if err != nil {
		var storeErr *ArtifactStoreError
		if errors.As(err, &storeErr) {
			r.setControlError(err)
			return err
		}
		return r.failAttempt(result, r.writer.redactor.Redact(err.Error()), false)
	}
	claimID, err := r.writer.entity("claim")
	if err != nil {
		return err
	}
	observationIDs := make([]string, 0, len(observations))
	artifactIDs := make([]string, 0)
	entries := make([]entry, 0, len(observations)*2+3)
	for _, observation := range observations {
		for _, artifact := range observation.Evidence {
			artifactIDs = append(artifactIDs, artifact.ID)
			entries = append(entries, entry{core.RecordArtifactStored, core.ArtifactStoredRecord{Artifact: artifact}})
		}
		observationIDs = append(observationIDs, observation.ID)
		entries = append(entries, entry{core.RecordObservationCreated, core.ObservationCreatedRecord{Observation: observation}})
	}
	claim := core.Claim{
		ID: claimID, RunID: r.id, TaskID: r.manifest.Task.ID, Kind: claimProposal.Kind,
		Description: claimProposal.Description, SourceIntentID: result.intent.ID,
		ObservationIDs: observationIDs, ArtifactIDs: artifactIDs, CreatedAt: now,
	}
	if err := core.ValidateClaim(claim); err != nil {
		return r.failAttempt(result, r.writer.redactor.Redact(err.Error()), false)
	}
	result.attempt.Phase = core.IntentAttemptConcluded
	entries = append(entries,
		entry{core.RecordClaimCreated, core.ClaimCreatedRecord{Claim: claim}},
		entry{core.RecordAttemptConcluded, core.AttemptConcludedRecord{AttemptID: result.attempt.ID, Conclusion: result.conclusion, At: now}},
		entry{core.RecordIntentConcluded, core.IntentConcludedRecord{IntentID: result.intent.ID, ResultClaimID: claim.ID, At: now}},
	)
	if _, err := r.writer.append(r.ctx, now, entries...); err != nil {
		return err
	}
	r.nonGrowth = 0
	return nil
}

func referencedObservations(report core.AttemptReport) []core.ObservationProposal {
	if report.Claim == nil || len(report.Claim.ObservationRefs) == 0 {
		return nil
	}
	byID := make(map[string]core.ObservationProposal, len(report.Observations))
	for _, observation := range report.Observations {
		byID[observation.LocalID] = observation
	}
	selected := make([]core.ObservationProposal, 0, len(report.Claim.ObservationRefs))
	for _, ref := range report.Claim.ObservationRefs {
		if observation, ok := byID[ref]; ok {
			selected = append(selected, observation)
		}
	}
	return selected
}

func (r *activeRun) failAttempt(result attemptResult, errorText string, control bool) error {
	now := r.now()
	result.attempt.Phase = core.IntentAttemptFailed
	result.attempt.Error = errorText
	result.attempt.FinishedAt = now
	entries := []entry{{core.RecordAttemptFailed, core.AttemptFailedRecord{AttemptID: result.attempt.ID, Error: errorText, At: now}}}
	attempts := len(r.attemptsFor(result.intent.ID)) + 1
	if attempts < r.policy().Exploration.MaxAttemptsPerIntent {
		entries = append(entries, entry{core.RecordIntentReopened, core.IntentReopenedRecord{IntentID: result.intent.ID, At: now}})
	} else {
		entries = append(entries, entry{core.RecordIntentAbandoned, core.IntentAbandonedRecord{IntentID: result.intent.ID, Reason: errorText, At: now}})
	}
	if _, err := r.writer.append(r.ctx, now, entries...); err != nil {
		return err
	}
	r.nonGrowth++
	if control {
		return fmt.Errorf("attempt failed: %s", errorText)
	}
	return nil
}

func (r *activeRun) attemptsFor(intentID string) []core.IntentAttempt {
	var out []core.IntentAttempt
	for _, attempt := range r.writer.boardSnapshot().Attempts {
		if attempt.IntentID == intentID {
			out = append(out, attempt)
		}
	}
	return out
}

func (r *activeRun) stagnate(reason string) error {
	if r.stopped {
		return nil
	}
	r.stopped = true
	r.stagnated = true
	state := r.writer.stateSnapshot()
	if _, err := r.writer.round(r.ctx, r.now(), state.Usage, r.nonGrowth, r.reasonFailures, true); err != nil {
		return err
	}
	return nil
}

func (r *activeRun) addUsage(ctx context.Context, delta core.Usage) error {
	if delta == (core.Usage{}) {
		return nil
	}
	state := r.writer.stateSnapshot()
	next := addUsage(state.Usage, delta)
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), 5*time.Second)
	defer cancel()
	if _, err := r.writer.round(recordCtx, r.now(), next, r.nonGrowth, r.reasonFailures, r.stagnated); err != nil {
		r.setControlError(err)
		return err
	}
	if breach := budgetBreach(r.policy().Budget, next); breach != nil {
		r.setBudgetError(breach)
		r.cancel()
	}
	return nil
}

func (r *activeRun) stopWork() {
	drain := r.policy().Exploration.DrainTimeout
	if drain <= 0 {
		drain = core.DefaultRunPolicy().Exploration.DrainTimeout
	}
	if slot := r.active; slot != nil {
		slot.cancel()
		select {
		case result := <-r.resultCh:
			r.active = nil
			_ = r.addUsage(context.Background(), result.usage)
			r.recordCanceledAttempt(result)
		case <-time.After(drain):
			r.active = nil
			r.recordCanceledSlot(slot)
		}
	}
	if r.reasonInFlight {
		select {
		case result := <-r.reasonCh:
			r.reasonInFlight = false
			_ = r.addUsage(context.Background(), result.usage)
		case <-time.After(drain):
		}
	}
}

func (r *activeRun) recordCanceledAttempt(result attemptResult) {
	result.attempt.Phase = core.IntentAttemptCancelled
	result.attempt.FinishedAt = r.now()
	if result.err != nil {
		result.attempt.Error = r.writer.redactor.Redact(result.err.Error())
	}
	r.recordCanceledSlot(&activeSlot{intent: result.intent, attempt: result.attempt})
}

func (r *activeRun) recordCanceledSlot(slot *activeSlot) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	now := r.now()
	attempt := slot.attempt
	attempt.Phase = core.IntentAttemptCancelled
	attempt.FinishedAt = now
	entries := []entry{{core.RecordAttemptCancelled, core.AttemptCancelledRecord{AttemptID: attempt.ID, At: now}}}
	board := r.writer.boardSnapshot()
	for _, intent := range board.Intents {
		if intent.ID == attempt.IntentID && intent.State == core.IntentClaimed {
			entries = append(entries, entry{core.RecordIntentAbandoned, core.IntentAbandonedRecord{IntentID: intent.ID, Reason: "run canceled before conclusion", At: now}})
			break
		}
	}
	if _, err := r.writer.append(ctx, now, entries...); err != nil {
		if !errors.Is(err, ErrRunClosed) {
			r.setControlError(err)
		}
	}
}

func (r *activeRun) cleanupSessions() error {
	var errs []error
	for _, sessionID := range r.sessionIDs {
		session := r.sessions[sessionID]
		if session == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), r.runner.closeTimeout)
		abortErr := session.Abort(ctx)
		closeErr := session.Close(ctx)
		cancel()
		if abortErr != nil {
			errs = append(errs, fmt.Errorf("abort session %s: %w", sessionID, abortErr))
		}
		if closeErr != nil {
			errs = append(errs, fmt.Errorf("close session %s: %w", sessionID, closeErr))
		}
	}
	return errors.Join(errs...)
}

func (r *activeRun) cleanupTarget() error {
	if !r.targetOwned || r.spec.Provisioner == nil {
		return nil
	}
	r.targetOwned = false
	ctx, cancel := context.WithTimeout(context.Background(), r.runner.closeTimeout)
	defer cancel()
	if err := r.spec.Provisioner.Close(ctx, r.target); err != nil {
		return fmt.Errorf("close target: %w", err)
	}
	return nil
}

func (r *activeRun) finish() error {
	r.stopWork()
	if err := errors.Join(r.cleanupSessions(), r.cleanupTarget()); err != nil {
		r.setControlError(err)
	}
	outcome, verdict, errorText, decisionErr := r.decide()
	if outcome == core.OutcomeSucceeded && (verdict == nil || !verdict.Success) {
		outcome, errorText = core.OutcomeFailed, "success requires a successful verifier verdict"
		decisionErr = errors.New(errorText)
	}
	finalizeErr := r.persistTerminal(outcome, verdict, errorText)
	result := core.ResultFrom(r.writer.stateSnapshot(), r.writer.boardSnapshot(), verdict)
	r.setResult(result, errors.Join(decisionErr, finalizeErr))
	r.cancel()
	return nil
}

func (r *activeRun) decide() (core.Outcome, *core.Verdict, string, error) {
	if err := r.controlError(); err != nil {
		return core.OutcomeFailed, nil, err.Error(), err
	}
	if breach := r.budgetError(); breach != nil {
		return core.OutcomeFailed, nil, breach.Error(), breach
	}
	if err := r.ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return core.OutcomeTimedOut, nil, "run wall time exceeded", err
		}
		return core.OutcomeCancelled, nil, "run canceled", err
	}
	if r.stagnated {
		return core.OutcomeStagnated, nil, "run stagnated without a verifier verdict", nil
	}
	if r.goalCandidate {
		return r.verify()
	}
	err := errors.New("run ended without a goal candidate")
	return core.OutcomeFailed, nil, err.Error(), err
}

func (r *activeRun) verify() (core.Outcome, *core.Verdict, string, error) {
	state := r.writer.stateSnapshot()
	switch state.Phase {
	case core.PhaseRunning:
		if _, err := r.writer.advance(r.ctx, core.Transition{Kind: core.TransitionGoalCandidate, At: r.now()}); err != nil {
			return core.OutcomeFailed, nil, err.Error(), err
		}
		state = r.writer.stateSnapshot()
	case core.PhaseVerifying:
	default:
		err := fmt.Errorf("run cannot verify from phase %q", state.Phase)
		return core.OutcomeFailed, nil, err.Error(), err
	}
	if !state.GoalCandidate {
		err := errors.New("verification requires a goal candidate")
		return core.OutcomeFailed, nil, err.Error(), err
	}
	if _, err := r.writer.append(r.ctx, r.now(), entry{core.RecordVerifierStarted, core.VerifierStartedRecord{}}); err != nil {
		return core.OutcomeFailed, nil, err.Error(), err
	}
	artifacts, err := r.runner.store.ListArtifacts(r.ctx, r.id)
	if err != nil {
		return core.OutcomeFailed, nil, err.Error(), err
	}
	evidenceDir, cleanup, err := r.runner.workspace.MaterializeEvidence(r.ctx, r.root, artifacts, r.runner.store.OpenArtifact)
	if err != nil {
		return core.OutcomeFailed, nil, err.Error(), err
	}
	if cleanup != nil {
		defer func() { _ = cleanup() }()
	}
	started := r.now()
	verdict, verifyErr := verifySafe(r.ctx, r.spec.Verifier, core.VerifyRequest{
		RunID: r.id, Task: r.manifest.Task, Target: r.target, EvidenceDir: evidenceDir,
	})
	duration := r.now().Sub(started)
	if verifyErr == nil {
		verdict.Authority = core.VerdictAuthorityVerifier
	}
	digest, _ := core.CanonicalJSON(verdict)
	sum := sha256.Sum256(digest)
	_, _ = r.writer.append(r.ctx, r.now(), entry{core.RecordVerifierCompleted, core.VerifierCompletedRecord{
		VerdictSHA256: hex.EncodeToString(sum[:]), DurationMs: duration.Milliseconds(),
	}})
	if err := r.ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return core.OutcomeTimedOut, nil, "run wall time exceeded", err
		}
		return core.OutcomeCancelled, nil, "run canceled", err
	}
	if verifyErr != nil {
		return core.OutcomeFailed, nil, verifyErr.Error(), verifyErr
	}
	if verdict.Success {
		return core.OutcomeSucceeded, &verdict, "", nil
	}
	message := strings.TrimSpace(verdict.Message)
	if message == "" {
		message = "verifier did not confirm the goal"
	}
	return core.OutcomeFailed, &verdict, message, nil
}

func verifySafe(ctx context.Context, verifier engine.Verifier, request core.VerifyRequest) (verdict core.Verdict, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("verifier panic: %v\n%s", recovered, debug.Stack())
		}
	}()
	if verifier == nil {
		return core.Verdict{}, errors.New("verifier is required")
	}
	return verifier.Verify(ctx, request)
}

func (r *activeRun) persistTerminal(outcome core.Outcome, verdict *core.Verdict, errorText string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), r.runner.finalizeWait)
	defer cancel()
	_, err := r.writer.finalize(ctx, r.now(), outcome, errorText, verdict)
	return err
}

func (r *activeRun) reasonRequest(board core.Board) core.ReasonRequest {
	intents := make([]core.Intent, 0, 1)
	for _, intent := range board.Intents {
		if intent.State == core.IntentOpen || intent.State == core.IntentClaimed {
			intents = append(intents, intent)
		}
	}
	if len(intents) > 1 {
		intents = intents[len(intents)-1:]
	}
	return core.ReasonRequest{
		Task: r.manifest.Task, Target: r.target,
		Claims: boundedClaims(board.Claims, r.policy()), Intents: intents,
		CanCreateIntent: true, BoardRevision: board.Revision, Policy: r.policy(),
	}
}

func (r *activeRun) exploreRequest(intent core.Intent) core.ExploreRequest {
	board := r.writer.boardSnapshot()
	return core.ExploreRequest{
		Task: r.manifest.Task, Target: r.target, Intent: intent,
		Claims: boundedClaims(board.Claims, r.policy()),
		Budget: core.AttemptBudget{
			MaxToolCalls: r.policy().Exploration.MaxToolCallsPerAttempt,
			MaxWallTime:  r.policy().Exploration.MaxWallTimePerAttempt,
		},
		WorkspaceDir:   r.dirs.SessionDir("executor"),
		MaxReportBytes: r.policy().Evidence.MaxReportBytes,
		BoardRevision:  board.Revision,
	}
}

func (r *activeRun) reasonStrategy() core.ReasonStrategy {
	if r.spec.ReasonStrategy != nil {
		return r.spec.ReasonStrategy
	}
	return engine.DefaultReasonStrategy()
}

func (r *activeRun) exploreStrategy() core.ExploreStrategy {
	if r.spec.ExploreStrategy != nil {
		return r.spec.ExploreStrategy
	}
	return engine.DefaultExploreStrategy()
}

func (r *activeRun) eventSink(ctx context.Context) engine.EventSink {
	return func(event engine.Event) error {
		if event.Type != core.EventAgent {
			return nil
		}
		var payload core.AgentEventPayload
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			return fmt.Errorf("decode agent event: %w", err)
		}
		_, err := r.writer.append(ctx, r.now(), entry{core.RecordAgentEvent, core.AgentEventRecord{
			NativeType: payload.NativeType, Data: payload.Data,
		}})
		if errors.Is(err, ErrRunClosed) || errors.Is(err, core.ErrTerminal) {
			return nil
		}
		if err != nil {
			r.setControlError(err)
		}
		return err
	}
}

func (r *activeRun) setControlError(err error) {
	if err == nil || errors.Is(err, ErrRunClosed) || errors.Is(err, core.ErrTerminal) {
		return
	}
	r.errMu.Lock()
	r.controlErr = errors.Join(r.controlErr, err)
	r.errMu.Unlock()
	r.cancel()
}

func (r *activeRun) controlError() error {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	return r.controlErr
}

func (r *activeRun) setBudgetError(err error) {
	if err == nil {
		return
	}
	r.errMu.Lock()
	if r.budgetErr == nil {
		r.budgetErr = err
	}
	r.errMu.Unlock()
}

func (r *activeRun) budgetError() error {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	return r.budgetErr
}

func (r *activeRun) setResult(result core.RunResult, err error) {
	r.resultMu.Lock()
	r.result = result
	r.resultErr = err
	r.resultMu.Unlock()
}

func (r *activeRun) resultSnapshot() (core.RunResult, error) {
	r.resultMu.RLock()
	defer r.resultMu.RUnlock()
	return r.result, r.resultErr
}

func budgetBreach(budget core.Budget, used core.Usage) error {
	totalTokens := used.TotalTokens
	if totalTokens == 0 {
		totalTokens = used.InputTokens + used.OutputTokens
	}
	switch {
	case budget.MaxTokens > 0 && totalTokens > budget.MaxTokens:
		return fmt.Errorf("token budget exceeded: %d > %d", totalTokens, budget.MaxTokens)
	case budget.MaxModelCalls > 0 && used.ModelCalls > budget.MaxModelCalls:
		return fmt.Errorf("model call budget exceeded: %d > %d", used.ModelCalls, budget.MaxModelCalls)
	case budget.MaxToolCalls > 0 && used.ToolCalls > budget.MaxToolCalls:
		return fmt.Errorf("tool call budget exceeded: %d > %d", used.ToolCalls, budget.MaxToolCalls)
	case budget.MaxCostUSD > 0 && used.CostUSD > budget.MaxCostUSD:
		return fmt.Errorf("cost budget exceeded: %.4f > %.4f", used.CostUSD, budget.MaxCostUSD)
	}
	return nil
}

func childContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(parent)
	}
	base, timeoutCancel := context.WithTimeout(parent, timeout)
	child, cancel := context.WithCancel(base)
	return child, func() { cancel(); timeoutCancel() }
}

func capturePanic(err *error) {
	if recovered := recover(); recovered != nil {
		*err = fmt.Errorf("panic: %v\n%s", recovered, debug.Stack())
	}
}

func boundedClaims(claims []core.Claim, policy core.RunPolicy) []core.Claim {
	limit := policy.Context.MaxBoardItems
	if limit <= 0 || limit > 64 {
		limit = 64
	}
	out := make([]core.Claim, 0, limit)
	for _, claim := range claims {
		if claim.Kind == core.ClaimOrigin || claim.Kind == core.ClaimGoal {
			out = append(out, claim)
		}
	}
	for _, claim := range claims {
		if len(out) >= limit {
			break
		}
		if claim.Kind == core.ClaimOrigin || claim.Kind == core.ClaimGoal {
			continue
		}
		out = append(out, claim)
	}
	var used int64
	bounded := out[:0]
	for _, claim := range out {
		claim.Description = truncate(claim.Description, policy.Context.MaxClaimBytes)
		cost := int64(len(claim.Description) + len(claim.ID) + 32)
		if len(bounded) >= 2 && policy.Context.MaxBoardBytes > 0 && used+cost > policy.Context.MaxBoardBytes {
			continue
		}
		used += cost
		bounded = append(bounded, claim)
	}
	return bounded
}

func truncate(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func buildEnvironment(runID, sessionID string, spec Spec, agent engine.AgentSpec, target core.TargetBinding, workspaceDir, taskDir, skillsDir string, secrets engine.SecretSource) map[string]string {
	env := map[string]string{
		"HARNESS_RUN_ID": runID, "HARNESS_SESSION_ID": sessionID, "HARNESS_TARGET_ID": target.TargetID,
		"HARNESS_WORKSPACE": workspaceDir, "HARNESS_TASK_DIR": taskDir, "HARNESS_SKILLS_DIR": skillsDir,
		"HOME": "/workspace",
	}
	for key, value := range agent.Env {
		env[key] = value
	}
	lookup := func(string) (string, bool) { return "", false }
	if secrets != nil {
		lookup = secrets.LookupEnv
	}
	for _, key := range agent.EnvFrom {
		if value, ok := lookup(key); ok {
			env[key] = value
		}
	}
	for _, key := range spec.SecretEnv {
		if value, ok := lookup(key); ok {
			env[key] = value
		}
	}
	return env
}
