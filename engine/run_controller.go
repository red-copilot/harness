package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/red-copilot/harness/core"
)

type runTransitionKind uint8

const (
	runTransitionStarted runTransitionKind = iota + 1
	runTransitionPhase
	runTransitionUsage
	runTransitionProgress
	runTransitionAuthorityCompletion
	runTransitionControlError
	runTransitionStagnation
	runTransitionAddSession
	runTransitionRemoveSession
	runTransitionBoardSnapshot
	runTransitionClaimFinalization
	runTransitionFinalized
)

type runTransition struct {
	kind          runTransitionKind
	at            time.Time
	phase         core.RunPhase
	outcome       core.RunOutcome
	usage         core.Usage
	completion    *core.Completion
	err           error
	sessionID     string
	session       AgentSession
	sessionStatus core.Status
	result        core.Result
	board         *boardSnapshot
}

type runTransitionResult struct {
	applied    bool
	validation error
	budgetErr  error
	frozen     *frozenRunState
}

type frozenRunState struct {
	usage      core.Usage
	startedAt  *time.Time
	sessions   []core.SessionResult
	controlErr error
	board      *boardSnapshot
}

type runControlState struct {
	phase          core.RunPhase
	outcome        core.RunOutcome
	startedAt      *time.Time
	sessions       map[string]AgentSession
	sessionHistory map[string]core.SessionResult
	usage          core.Usage
	authority      *core.Completion
	progress       core.Completion
	stagnated      bool
	budgetErr      error
	controlErr     error
	result         core.Result
	err            error
	board          *boardSnapshot
	terminalClaim  bool
	terminalDone   bool
}

// runController is the single writer for one Run's lifecycle state.
type runController struct {
	runner *Runner
	id     string
	ctx    context.Context
	cancel context.CancelFunc
	events chan core.Event
	hints  chan core.Hint
	done   chan struct{}

	root              string
	taskDir           string
	skillsDir         string
	redaction         *core.Redactor
	budget            core.Budget
	materializeSkills func(string) error

	watchCancel context.CancelFunc
	watchDone   chan struct{}

	eventMu      sync.Mutex
	eventsClosed bool

	mu            sync.RWMutex
	state         runControlState
	transitionSeq uint64
}

func newRunController(runner *Runner, id string, ctx context.Context, cancel context.CancelFunc, root string, budget core.Budget, redaction *core.Redactor, materializeSkills func(string) error) *runController {
	if redaction == nil {
		redaction = core.NewRedactor()
	}
	return &runController{
		runner: runner, id: id, ctx: ctx, cancel: cancel,
		events: make(chan core.Event, 256), done: make(chan struct{}),
		root: root, taskDir: filepath.Join(root, "task"), skillsDir: filepath.Join(root, "skills"),
		redaction: redaction, budget: budget, materializeSkills: materializeSkills,
		state: runControlState{phase: core.RunPhaseQueued, sessions: map[string]AgentSession{}, sessionHistory: map[string]core.SessionResult{}},
	}
}

func (c *runController) apply(transition runTransition) runTransitionResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.transitionSeq++
	if c.state.terminalDone || (c.state.terminalClaim && transition.kind != runTransitionFinalized) {
		return runTransitionResult{}
	}
	switch transition.kind {
	case runTransitionStarted:
		if c.state.startedAt == nil && !transition.at.IsZero() {
			at := transition.at
			c.state.startedAt = &at
		}
	case runTransitionPhase:
		if err := core.ValidateRunState(transition.phase, ""); err != nil || transition.phase == core.RunPhaseTerminal {
			if err == nil {
				err = errors.New("terminal phase requires finalize")
			}
			return runTransitionResult{validation: err}
		}
		if err := core.ValidateRunTransition(c.state.phase, transition.phase); err != nil {
			return runTransitionResult{validation: err}
		}
		c.state.phase = transition.phase
		c.state.outcome = ""
	case runTransitionUsage:
		c.state.usage.InputTokens += transition.usage.InputTokens
		c.state.usage.OutputTokens += transition.usage.OutputTokens
		c.state.usage.TotalTokens += transition.usage.TotalTokens
		c.state.usage.ModelCalls += transition.usage.ModelCalls
		c.state.usage.ToolCalls += transition.usage.ToolCalls
		c.state.usage.CostUSD += transition.usage.CostUSD
		if breach := budgetBreach(c.budget, c.state.usage); breach != nil && c.state.budgetErr == nil {
			c.state.budgetErr = breach
		}
		return runTransitionResult{applied: true, budgetErr: c.state.budgetErr}
	case runTransitionProgress:
		if transition.completion != nil {
			c.state.progress = *transition.completion
		}
	case runTransitionAuthorityCompletion:
		if transition.completion != nil && c.state.authority == nil {
			completion := *transition.completion
			c.state.authority = &completion
		}
	case runTransitionControlError:
		if transition.err != nil && c.state.controlErr == nil {
			c.state.controlErr = transition.err
		}
	case runTransitionStagnation:
		c.state.stagnated = true
	case runTransitionAddSession:
		if transition.session != nil {
			c.state.sessions[transition.sessionID] = transition.session
		}
	case runTransitionRemoveSession:
		if session := c.state.sessions[transition.sessionID]; session != nil {
			result := core.SessionResult{ID: transition.sessionID, Status: transition.sessionStatus, Usage: session.Usage()}
			c.state.sessionHistory[transition.sessionID] = result
			delete(c.state.sessions, transition.sessionID)
		}
	case runTransitionBoardSnapshot:
		if transition.board != nil {
			copy := *transition.board
			c.state.board = &copy
		}
	case runTransitionClaimFinalization:
		if err := core.ValidateRunState(core.RunPhaseTerminal, transition.outcome); err != nil {
			return runTransitionResult{validation: err}
		}
		c.state.outcome = transition.outcome
		c.state.terminalClaim = true
		return runTransitionResult{applied: true, frozen: c.frozenLocked()}
	case runTransitionFinalized:
		c.state.outcome = transition.outcome
		c.state.result = transition.result
		c.state.err = transition.err
		c.state.terminalDone = true
	default:
		return runTransitionResult{validation: errors.New("unknown run transition")}
	}
	return runTransitionResult{applied: true}
}

func (c *runController) frozenLocked() *frozenRunState {
	startedAt := c.state.startedAt
	if startedAt != nil {
		at := *startedAt
		startedAt = &at
	}
	state := &frozenRunState{usage: c.state.usage, startedAt: startedAt, sessions: c.sessionResultsLocked(core.StatusSucceeded), controlErr: c.state.controlErr}
	if c.state.board != nil {
		copy := *c.state.board
		state.board = &copy
	}
	return state
}

func (c *runController) sessionResultsLocked(status core.Status) []core.SessionResult {
	ids := make([]string, 0, len(c.state.sessions)+len(c.state.sessionHistory))
	seen := map[string]struct{}{}
	for id := range c.state.sessions {
		ids = append(ids, id)
		seen[id] = struct{}{}
	}
	for id := range c.state.sessionHistory {
		if _, ok := seen[id]; !ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := make([]core.SessionResult, 0, len(ids))
	for _, id := range ids {
		if session := c.state.sessions[id]; session != nil {
			out = append(out, core.SessionResult{ID: id, Status: status, Usage: session.Usage()})
		} else {
			out = append(out, c.state.sessionHistory[id])
		}
	}
	return out
}

func (c *runController) addUsage(usage core.Usage) error {
	result := c.apply(runTransition{kind: runTransitionUsage, usage: usage})
	if !result.applied {
		return ErrRunClosed
	}
	if result.budgetErr != nil {
		c.cancel()
		return result.budgetErr
	}
	return nil
}

func (c *runController) backendCompletion() *core.Completion {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.state.authority == nil {
		return nil
	}
	copy := *c.state.authority
	return &copy
}

func (c *runController) setBackendCompletion(completion core.Completion) bool {
	return c.apply(runTransition{kind: runTransitionAuthorityCompletion, completion: &completion}).applied
}

func (c *runController) progress() core.Completion {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state.progress
}

func (c *runController) setProgress(completion core.Completion) {
	_ = c.apply(runTransition{kind: runTransitionProgress, completion: &completion})
}

func (c *runController) setControlError(err error) {
	_ = c.apply(runTransition{kind: runTransitionControlError, err: err})
	c.cancel()
}

func (c *runController) stagnated() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state.stagnated
}

func (c *runController) setStagnated() {
	_ = c.apply(runTransition{kind: runTransitionStagnation})
}

func (c *runController) budgetExceeded() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state.budgetErr
}

func (c *runController) addSession(id string, session AgentSession) {
	_ = c.apply(runTransition{kind: runTransitionAddSession, sessionID: id, session: session})
}

func (c *runController) removeSession(id string, status core.Status) {
	_ = c.apply(runTransition{kind: runTransitionRemoveSession, sessionID: id, sessionStatus: status})
}

func (c *runController) sessionResults() []core.SessionResult {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sessionResultsLocked(core.StatusRunning)
}

func (c *runController) sessionWorkspace(sessionID string) string {
	return filepath.Join(c.root, sessionID)
}

func (c *runController) redactor() *core.Redactor { return c.redaction }

func (c *runController) waitResult() (core.Result, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state.result, c.state.err
}

func (c *runController) canonicalEvent(source string, typ core.EventType, payload any) (core.Event, error) {
	event, err := core.NewEvent(c.id, typ, payload)
	if err != nil {
		return core.Event{}, err
	}
	event.Source = source
	return event, nil
}

func (c *runController) emitEvent(event core.Event) error {
	version, known := core.EventSchemaVersion(event.Type)
	if !known {
		return fmt.Errorf("unknown canonical event type %q", event.Type)
	}
	if event.SchemaVersion == 0 {
		event.SchemaVersion = version
	}
	if event.SchemaVersion > version {
		return fmt.Errorf("event %q schema version %d is newer than supported %d", event.Type, event.SchemaVersion, version)
	}
	if len(event.Data) == 0 || !json.Valid(event.Data) {
		return fmt.Errorf("event %q requires a valid JSON payload", event.Type)
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	c.eventMu.Lock()
	defer c.eventMu.Unlock()
	if c.eventsClosed {
		return ErrRunClosed
	}
	c.mu.RLock()
	claimed := c.state.terminalClaim
	c.mu.RUnlock()
	if claimed {
		return ErrRunClosed
	}
	stored, err := c.runner.store.AppendEvent(context.Background(), event)
	if err != nil {
		c.setControlError(err)
		return fmt.Errorf("persist event %q: %w", event.Type, err)
	}
	select {
	case c.events <- stored:
	default:
	}
	return nil
}

func (c *runController) appendLog(record core.LogRecord) error {
	if record.Time.IsZero() {
		record.Time = time.Now().UTC()
	}
	c.eventMu.Lock()
	defer c.eventMu.Unlock()
	if c.eventsClosed {
		return ErrRunClosed
	}
	c.mu.RLock()
	claimed := c.state.terminalClaim
	c.mu.RUnlock()
	if claimed {
		return ErrRunClosed
	}
	if _, err := c.runner.store.AppendLog(context.Background(), record); err != nil {
		c.setControlError(err)
		return fmt.Errorf("persist log: %w", err)
	}
	return nil
}

func (c *runController) updatePhase(phase core.RunPhase) error {
	phase, _, _, err := core.NormalizeRunState(phase, "", "")
	if err != nil {
		return err
	}
	if result := c.apply(runTransition{kind: runTransitionPhase, phase: phase}); !result.applied {
		if result.validation != nil {
			return result.validation
		}
		return ErrRunClosed
	}
	record, err := c.runner.store.GetRun(context.Background(), c.id)
	if err != nil {
		c.setControlError(err)
		return err
	}
	now := time.Now().UTC()
	record.Phase, record.Outcome = phase, ""
	if phase == core.RunPhaseProvisioning {
		record.StartedAt = &now
	}
	if err := c.runner.store.UpdateRun(context.Background(), record); err != nil {
		c.setControlError(err)
		return err
	}
	event, err := c.canonicalEvent("harness", core.EventRunPhase, core.RunPhasePayload{Phase: phase})
	if err != nil {
		return err
	}
	event.Time = now
	return c.emitEvent(event)
}

func (c *runController) setBoardSnapshot(snapshot boardSnapshot) {
	copy := snapshot
	_ = c.apply(runTransition{kind: runTransitionBoardSnapshot, board: &copy})
}

func (c *runController) finishExecution(outcome core.RunOutcome, finalErr error, verdict *core.Verdict) {
	if err := core.ValidateRunState(core.RunPhaseTerminal, outcome); err != nil {
		finalErr, outcome = err, core.RunOutcomeFailed
	}
	if outcome == core.RunOutcomeSucceeded && (verdict == nil || !verdict.Success) {
		finalErr, outcome = errors.New("successful run requires a successful authoritative verdict"), core.RunOutcomeFailed
	}
	c.eventMu.Lock()
	claim := c.apply(runTransition{kind: runTransitionClaimFinalization, outcome: outcome})
	c.eventMu.Unlock()
	if !claim.applied {
		return
	}
	frozen := claim.frozen
	redactor := c.redactor()
	errorText := redactError(redactor, finalErr)
	result := core.Result{RunID: c.id, Phase: core.RunPhaseTerminal, Outcome: outcome, Verdict: verdict}
	if frozen.board != nil {
		result.Claims = append([]core.Claim(nil), frozen.board.Claims...)
		result.Intents = append([]core.Intent(nil), frozen.board.Intents...)
		result.Hints = append([]core.Hint(nil), frozen.board.Hints...)
		result.IntentAttempts = append([]core.IntentAttempt(nil), frozen.board.Attempts...)
		result.Observations = append([]core.Observation(nil), frozen.board.Observations...)
		result.Artifacts = append([]core.Artifact(nil), frozen.board.Artifacts...)
	}
	result.Sessions = append([]core.SessionResult(nil), frozen.sessions...)
	result.Usage = usageFromSessions(result.Sessions)
	if result.Usage == (core.Usage{}) {
		result.Usage = frozen.usage
	}
	result.StartedAt = frozen.startedAt
	now := time.Now().UTC()
	result.FinishedAt = &now
	result.Error = errorText
	if finalErr == nil && frozen.controlErr != nil {
		finalErr = frozen.controlErr
		outcome = core.RunOutcomeFailed
		result.Outcome = outcome
		result.Error = redactError(redactor, frozen.controlErr)
	}
	result = core.RedactResult(redactor, result)
	record, getErr := c.runner.store.GetRun(context.Background(), c.id)
	if getErr != nil {
		outcome = core.RunOutcomeFailed
		finalErr = getErr
		result.Outcome = outcome
		result.Error = redactError(redactor, getErr)
		record = core.RunRecord{ID: c.id}
	}
	record.Phase, record.Outcome = core.RunPhaseTerminal, outcome
	record.Result = mustJSON(result)
	record.Error = result.Error
	record.FinishedAt = &now
	var verdictRecord *core.VerdictRecord
	if verdict != nil {
		verdictRecord = &core.VerdictRecord{RunID: c.id, Success: verdict.Success, Score: verdict.Score, Message: redactor.Redact(verdict.Message), Evidence: redactor.RedactJSON(verdict.Evidence), CreatedAt: now}
	}
	c.eventMu.Lock()
	finalEvent, eventErr := c.canonicalEvent("harness", core.EventRunFinalized, core.RunFinalizedPayload{Outcome: outcome, Error: result.Error})
	finalEvent.Time = now
	var stored core.Event
	finalizeErr := eventErr
	if finalizeErr == nil {
		stored, finalizeErr = c.runner.store.Finalize(context.Background(), FinalizationRecord{Run: record, Verdict: verdictRecord, Event: finalEvent})
	}
	if finalizeErr != nil {
		outcome = core.RunOutcomeFailed
		finalErr = finalizeErr
		result.Outcome = outcome
		result.Error = redactError(redactor, finalizeErr)
	}
	if !c.eventsClosed {
		if finalizeErr == nil {
			select {
			case c.events <- stored:
			default:
			}
		}
		c.eventsClosed = true
		close(c.events)
	}
	c.eventMu.Unlock()
	c.apply(runTransition{kind: runTransitionFinalized, outcome: outcome, err: finalErr, result: result})
	select {
	case <-c.done:
	default:
		close(c.done)
	}
}

func (c *runController) run(spec RunSpec) {
	defer c.cancel()
	_ = c.apply(runTransition{kind: runTransitionStarted, at: time.Now().UTC()})
	if err := ensureDir(c.taskDir); err != nil {
		c.finishExecution(core.RunOutcomeFailed, err, nil)
		return
	}
	if err := ensureDir(c.skillsDir); err != nil {
		c.finishExecution(core.RunOutcomeFailed, err, nil)
		return
	}
	if c.materializeSkills != nil {
		if err := c.materializeSkills(c.skillsDir); err != nil {
			c.finishExecution(core.RunOutcomeFailed, err, nil)
			return
		}
	}
	if err := c.updatePhase(core.RunPhaseProvisioning); err != nil {
		c.finishExecution(core.RunOutcomeFailed, err, nil)
		return
	}
	target := core.TargetBinding{TargetID: core.TargetFingerprint(spec.Target), Addresses: append([]string(nil), spec.Target.Addresses...), Isolation: spec.Target.Isolation}
	if spec.Provisioner != nil {
		provisioned, err := spec.Provisioner.Provision(c.ctx, core.ProvisionRequest{RunID: c.id, Task: spec.Task, Target: spec.Target})
		if err != nil {
			c.finishExecution(core.RunOutcomeFailed, err, nil)
			return
		}
		if provisioned.TargetID == "" {
			provisioned.TargetID = core.TargetFingerprint(spec.Target)
		}
		if len(provisioned.Addresses) == 0 {
			provisioned.Addresses = append([]string(nil), spec.Target.Addresses...)
		}
		if provisioned.Isolation == "" {
			provisioned.Isolation = spec.Target.Isolation
		}
		if err := core.ValidateTargetBinding(spec.Target, provisioned); err != nil {
			c.finishExecution(core.RunOutcomeFailed, err, nil)
			return
		}
		target = provisioned
	}
	provisionedEvent, err := c.canonicalEvent("harness", core.EventRunProvisioned, core.RunProvisionedPayload{Target: target})
	if err != nil {
		c.finishExecution(core.RunOutcomeFailed, err, nil)
		return
	}
	if err := c.emitEvent(provisionedEvent); err != nil {
		c.finishExecution(core.RunOutcomeFailed, err, nil)
		return
	}
	if err := c.updatePhase(core.RunPhaseRunning); err != nil {
		c.finishExecution(core.RunOutcomeFailed, err, nil)
		return
	}
	if spec.Backend != nil {
		c.startBackendWatcher(spec.Backend, target)
	}
	explorationErr := newBlackboardEngine(c.runner, c, spec, target).run(c.ctx)
	if watchErr := c.stopBackendWatcher(); watchErr != nil {
		explorationErr = errors.Join(explorationErr, watchErr)
	}
	if spec.Backend != nil && c.ctx.Err() == nil && c.backendCompletion() == nil {
		if authorityErr := c.observeAuthorityOnce(spec.Backend, target); authorityErr != nil {
			explorationErr = errors.Join(explorationErr, authorityErr)
		}
	}
	outcome := core.RunOutcomeFailed
	var finalErr error
	var verdict *core.Verdict
	if breach := c.budgetExceeded(); breach != nil {
		finalErr = breach
	} else if completion := c.backendCompletion(); completion != nil {
		verdict = completionVerdict(*completion)
		if completion.Success && (explorationErr == nil || errors.Is(explorationErr, context.Canceled)) {
			outcome = core.RunOutcomeSucceeded
		} else if explorationErr != nil && !errors.Is(explorationErr, context.Canceled) {
			finalErr = explorationErr
		}
	} else if c.ctx.Err() != nil {
		finalErr = c.ctx.Err()
		if c.stagnated() {
			outcome = core.RunOutcomeStagnated
		} else if errors.Is(c.ctx.Err(), context.DeadlineExceeded) {
			outcome = core.RunOutcomeTimedOut
		} else {
			outcome = core.RunOutcomeCancelled
		}
	} else if explorationErr != nil {
		finalErr = explorationErr
	} else if spec.Verifier != nil {
		if err := c.updatePhase(core.RunPhaseFinalizing); err != nil {
			finalErr = err
		} else if err := c.updatePhase(core.RunPhaseVerifying); err != nil {
			finalErr = err
		} else {
			evidenceDir, bundleErr := buildEvidenceBundle(c.ctx, c.runner.store, c.id, c.root)
			verifyVerdict, verifyErr := core.Verdict{}, bundleErr
			if bundleErr == nil {
				verifyVerdict, verifyErr = spec.Verifier.Verify(c.ctx, core.VerifyRequest{RunID: c.id, Task: spec.Task, Target: target, EvidenceDir: evidenceDir})
				_ = os.RemoveAll(evidenceDir)
			}
			if verifyErr == nil {
				verifyVerdict.Authority = core.VerdictAuthorityVerifier
			}
			if spec.Backend != nil && c.ctx.Err() == nil {
				if authorityErr := c.observeAuthorityOnce(spec.Backend, target); authorityErr != nil {
					verifyErr = errors.Join(verifyErr, authorityErr)
				}
			}
			if completion := c.backendCompletion(); completion != nil {
				verdict = completionVerdict(*completion)
				if completion.Success {
					outcome = core.RunOutcomeSucceeded
				} else {
					outcome = core.RunOutcomeFailed
				}
			} else if verifyErr != nil {
				finalErr = verifyErr
			} else {
				verdict = &verifyVerdict
				if verifyVerdict.Success {
					outcome = core.RunOutcomeSucceeded
				}
			}
		}
	} else if c.stagnated() {
		outcome = core.RunOutcomeStagnated
	} else {
		finalErr = errors.New("run has no authoritative verdict")
	}
	if spec.Provisioner != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), c.runner.closeTimeout)
		if closeErr := spec.Provisioner.Close(closeCtx, target); closeErr != nil {
			finalErr = errors.Join(finalErr, fmt.Errorf("close backend: %w", closeErr))
			if outcome == core.RunOutcomeSucceeded {
				outcome = core.RunOutcomeFailed
			}
		}
		cancel()
	}
	c.finishExecution(outcome, finalErr, verdict)
	c.runner.mu.Lock()
	delete(c.runner.runs, c.id)
	c.runner.mu.Unlock()
}

func completionVerdict(completion core.Completion) *core.Verdict {
	return &core.Verdict{Authority: core.VerdictAuthorityBackend, Success: completion.Success, Score: completion.Score, Message: completion.Message, Evidence: mustJSON(completion.Metadata)}
}

func (c *runController) startBackendWatcher(backend CompletionSource, target core.TargetBinding) {
	ctx, cancel := context.WithCancel(c.ctx)
	c.watchCancel = cancel
	c.watchDone = make(chan struct{})
	go func() {
		defer close(c.watchDone)
		c.watchBackend(ctx, backend, target)
	}()
}

func (c *runController) stopBackendWatcher() error {
	if c.watchCancel == nil {
		return nil
	}
	c.watchCancel()
	select {
	case <-c.watchDone:
		c.watchCancel, c.watchDone = nil, nil
		return nil
	case <-time.After(c.runner.closeTimeout):
		return errors.New("backend watcher did not stop")
	}
}

func (c *runController) observeAuthorityOnce(backend CompletionSource, target core.TargetBinding) error {
	completion, err := backend.Observe(c.ctx, target)
	if err != nil {
		event, eventErr := c.canonicalEvent("authority", core.EventAuthorityObserveError, core.AuthorityErrorPayload{Error: c.redactor().Redact(err.Error())})
		if eventErr != nil {
			return eventErr
		}
		emitErr := c.emitEvent(event)
		if emitErr != nil && !errors.Is(emitErr, ErrRunClosed) {
			return emitErr
		}
		return nil
	}
	c.setProgress(completion)
	if completion.Done && c.backendCompletion() == nil {
		c.setBackendCompletion(completion)
		event, eventErr := c.canonicalEvent("authority", core.EventAuthorityCompleted, core.AuthorityProgressPayload{Completion: completion})
		if eventErr != nil {
			return eventErr
		}
		if err := c.emitEvent(event); err != nil && !errors.Is(err, ErrRunClosed) {
			return err
		}
	}
	return nil
}

func (c *runController) watchBackend(ctx context.Context, backend CompletionSource, target core.TargetBinding) {
	ticker := time.NewTicker(c.runner.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		completion, err := backend.Observe(ctx, target)
		if err != nil {
			event, eventErr := c.canonicalEvent("authority", core.EventAuthorityObserveError, core.AuthorityErrorPayload{Error: c.redactor().Redact(err.Error())})
			if eventErr != nil {
				c.setControlError(eventErr)
				return
			}
			if emitErr := c.emitEvent(event); emitErr != nil {
				if !errors.Is(emitErr, ErrRunClosed) {
					c.setControlError(emitErr)
				}
				return
			}
			continue
		}
		c.setProgress(completion)
		if completion.Done {
			if c.backendCompletion() == nil {
				c.setBackendCompletion(completion)
				if event, eventErr := c.canonicalEvent("authority", core.EventAuthorityCompleted, core.AuthorityProgressPayload{Completion: completion}); eventErr == nil {
					_ = c.emitEvent(event)
				}
			}
			c.cancel()
			return
		}
	}
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

func usageFromSessions(sessions []core.SessionResult) core.Usage {
	var total core.Usage
	for _, session := range sessions {
		total.InputTokens += session.Usage.InputTokens
		total.OutputTokens += session.Usage.OutputTokens
		total.TotalTokens += session.Usage.TotalTokens
		total.ModelCalls += session.Usage.ModelCalls
		total.ToolCalls += session.Usage.ToolCalls
		total.CostUSD += session.Usage.CostUSD
	}
	return total
}

var ErrRunClosed = errors.New("run event stream closed")

// commitBoard atomically persists one board revision and publishes the stored
// events to the live audit stream.
func (c *runController) commitBoard(ctx context.Context, commit Commit) error {
	commit.RunID = c.id
	c.eventMu.Lock()
	defer c.eventMu.Unlock()
	if c.eventsClosed {
		return ErrRunClosed
	}
	c.mu.RLock()
	claimed := c.state.terminalClaim
	c.mu.RUnlock()
	if claimed {
		return ErrRunClosed
	}
	stored, err := c.runner.store.Commit(ctx, commit)
	if err != nil {
		c.setControlError(err)
		return err
	}
	for _, event := range stored {
		select {
		case c.events <- event:
		default:
		}
	}
	return nil
}
