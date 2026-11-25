package kernel

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/red-copilot/harness/core"
	"github.com/red-copilot/harness/engine"
)

// ErrRunClosed is returned when a Run's journal has already been finalized.
var ErrRunClosed = errors.New("run journal closed")

type entry struct {
	typ     core.RecordType
	payload any
}

// writer is the single writer for one Run's journal, state and board. Every
// method takes the writer lock, seals the batch, appends it atomically and only
// then adopts the returned state. A failed append never mutates local state.
type writer struct {
	store    engine.V5Store
	redactor *core.Redactor
	clock    engine.Clock
	manifest core.Manifest
	digest   string
	runID    string

	mu     sync.Mutex
	state  core.State
	board  core.Board
	closed bool

	publish func(core.Record)

	entityMu   sync.Mutex
	entityBase uint64
	entitySeq  uint64
	newEntity  func(runID, kind string) (string, error)
}

func newWriter(store engine.V5Store, redactor *core.Redactor, clock engine.Clock, manifest core.Manifest, digest string, state core.State, board core.Board, publish func(core.Record)) *writer {
	return &writer{
		store: store, redactor: redactor, clock: clock, manifest: manifest, digest: digest,
		runID: manifest.RunID, state: state, board: board, publish: publish,
		entityBase: state.HeadSeq * 1_000_000,
	}
}

func (w *writer) now() time.Time {
	if w.clock == nil {
		return time.Time{}
	}
	return w.clock.Now().UTC()
}

// entity returns an opaque, Run-unique identity. IDs are never parsed, so a
// resumed Run cannot collide with historical identifiers.
func (w *writer) entity(kind string) (string, error) {
	if w.newEntity != nil {
		return w.newEntity(w.runID, kind)
	}
	w.entityMu.Lock()
	defer w.entityMu.Unlock()
	w.entitySeq++
	return fmt.Sprintf("%s:%s:%d", w.runID, kind, w.entityBase+w.entitySeq), nil
}

func (w *writer) stateSnapshot() core.State {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state
}

func (w *writer) boardSnapshot() core.Board {
	w.mu.Lock()
	defer w.mu.Unlock()
	board := w.board
	board.Claims = append([]core.Claim(nil), w.board.Claims...)
	board.Intents = append([]core.Intent(nil), w.board.Intents...)
	board.Attempts = append([]core.IntentAttempt(nil), w.board.Attempts...)
	board.Observations = append([]core.Observation(nil), w.board.Observations...)
	board.Artifacts = append([]core.Artifact(nil), w.board.Artifacts...)
	return board
}

// advance applies one state-machine transition and its records atomically.
func (w *writer) advance(ctx context.Context, transition core.Transition, entries ...entry) (core.State, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return core.State{}, ErrRunClosed
	}
	if transition.At.IsZero() {
		transition.At = w.now()
	}
	next, err := core.Advance(w.state, transition)
	if err != nil {
		return core.State{}, err
	}
	records, projected, err := w.compose(next, transition.At, true, true, transition.Kind == core.TransitionGoalCandidate, entries)
	if err != nil {
		return core.State{}, err
	}
	next.BoardRevision = projected.Revision
	next.HeadSeq, next.HeadHash = 0, ""
	sealed, err := w.redactor.SealBatch(core.Batch{
		RunID: w.runID, ExpectedHead: w.state.HeadSeq, State: &next,
		BoardRevision: projected.Revision, Records: records,
	})
	if err != nil {
		return core.State{}, err
	}
	result, err := w.store.Append(ctx, sealed)
	if err != nil {
		return core.State{}, err
	}
	return w.commit(result)
}

// append records a board or audit change without a lifecycle transition.
func (w *writer) append(ctx context.Context, at time.Time, entries ...entry) (core.State, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return core.State{}, ErrRunClosed
	}
	if at.IsZero() {
		at = w.now()
	}
	next := w.state
	records, projected, err := w.compose(next, at, false, false, false, entries)
	if err != nil {
		return core.State{}, err
	}
	next.BoardRevision = projected.Revision
	next.HeadSeq, next.HeadHash = 0, ""
	sealed, err := w.redactor.SealBatch(core.Batch{
		RunID: w.runID, ExpectedHead: w.state.HeadSeq, State: &next,
		BoardRevision: projected.Revision, Records: records,
	})
	if err != nil {
		return core.State{}, err
	}
	result, err := w.store.Append(ctx, sealed)
	if err != nil {
		return core.State{}, err
	}
	return w.commit(result)
}

// round records usage and exploration counters. It is used between turns and
// deliberately does not require a lifecycle transition.
func (w *writer) round(ctx context.Context, at time.Time, usage core.Usage, nonGrowth, reasonFailures int, stagnated bool) (core.State, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return core.State{}, ErrRunClosed
	}
	if !usageAtLeast(usage, w.state.Usage) {
		return core.State{}, errors.New("usage must not decrease")
	}
	next := w.state
	next.Usage = usage
	next.NonGrowthRounds = nonGrowth
	next.ReasonFailures = reasonFailures
	next.Stagnated = next.Stagnated || stagnated
	if err := next.Validate(); err != nil {
		return core.State{}, err
	}
	records, projected, err := w.compose(next, at, false, true, false, nil)
	if err != nil {
		return core.State{}, err
	}
	next.BoardRevision = projected.Revision
	next.HeadSeq, next.HeadHash = 0, ""
	sealed, err := w.redactor.SealBatch(core.Batch{
		RunID: w.runID, ExpectedHead: w.state.HeadSeq, State: &next,
		BoardRevision: projected.Revision, Records: records,
	})
	if err != nil {
		return core.State{}, err
	}
	result, err := w.store.Append(ctx, sealed)
	if err != nil {
		return core.State{}, err
	}
	return w.commit(result)
}

// resume appends the replayable resume normalization record.
func (w *writer) resume(ctx context.Context, at time.Time) (core.State, error) {
	return w.append(ctx, at, entry{core.RecordRunResumed, core.RunResumedRecord{
		HeadSeq: w.stateSnapshot().HeadSeq, Normalizations: core.ResumeNormalizations,
	}})
}

// finalize writes the terminal state, verdict and records atomically.
func (w *writer) finalize(ctx context.Context, at time.Time, outcome core.Outcome, errorText string, verdict *core.Verdict, entries ...entry) (core.State, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return core.State{}, ErrRunClosed
	}
	if at.IsZero() {
		at = w.now()
	}
	transition := core.Transition{Kind: core.TransitionFinalize, At: at, Outcome: outcome, Verdict: verdict, Error: errorText, Usage: w.state.Usage}
	next, err := core.Advance(w.state, transition)
	if err != nil {
		return core.State{}, err
	}
	all := append([]entry(nil), entries...)
	if outcome == core.OutcomeInterrupted {
		all = append(all, entry{core.RecordRunInterrupted, core.RunInterruptedRecord{Reason: errorText, At: at}})
	} else {
		all = append(all, entry{core.RecordRunFinalized, core.RunFinalizedRecord{Outcome: outcome, Error: errorText, Usage: next.Usage, At: at}})
	}
	records, projected, err := w.compose(next, at, false, false, false, all)
	if err != nil {
		return core.State{}, err
	}
	next.BoardRevision = projected.Revision
	next.HeadSeq, next.HeadHash = 0, ""
	sealed, err := w.redactor.SealFinalization(core.Finalization{
		RunID: w.runID, ExpectedHead: w.state.HeadSeq, Outcome: outcome, Error: errorText,
		Usage: next.Usage, Verdict: verdict, Records: records, FinishedAt: at,
	})
	if err != nil {
		return core.State{}, err
	}
	result, err := w.store.Finalize(ctx, sealed)
	if err != nil {
		return core.State{}, err
	}
	state, err := w.commit(result)
	if err != nil {
		return core.State{}, err
	}
	w.closed = true
	return state, nil
}

func (w *writer) appendLog(ctx context.Context, record core.LogRecord) error {
	sealed := w.redactor.SealLog(record)
	return w.store.AppendLogs(ctx, w.runID, []core.SealedLog{sealed})
}

func (w *writer) closedNow() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}

// compose builds the record chain and the projected board. Callers hold mu.
func (w *writer) compose(next core.State, at time.Time, autoPhase, autoState, goal bool, entries []entry) ([]core.Record, core.Board, error) {
	all := make([]entry, 0, len(entries)+4)
	if autoState && stateProjectionChanged(w.state, next) {
		all = append(all, entry{core.RecordRunState, core.RunStateRecord{
			Usage: next.Usage, GoalCandidate: next.GoalCandidate,
			NonGrowthRounds: next.NonGrowthRounds, ReasonFailures: next.ReasonFailures,
			Stagnated: next.Stagnated,
		}})
	}
	if autoPhase && next.Phase != w.state.Phase {
		all = append(all, entry{core.RecordRunPhase, core.RunPhaseRecord{From: w.state.Phase, To: next.Phase, At: at}})
	}
	if goal {
		all = append(all, entry{core.RecordRunGoalCandidate, core.RunGoalCandidateRecord{At: at}})
	}
	all = append(all, entries...)

	prev := w.state.HeadHash
	seq := w.state.HeadSeq + 1
	board := cloneBoard(w.board)
	records := make([]core.Record, 0, len(all))
	for i, item := range all {
		record, err := core.NewRecord(w.runID, seq, item.typ, at, item.payload, prev)
		if err != nil {
			return nil, core.Board{}, fmt.Errorf("record %d (%s): %w", i, item.typ, err)
		}
		if err := board.Apply(record); err != nil {
			return nil, core.Board{}, fmt.Errorf("project record %d (%s): %w", i, item.typ, err)
		}
		records = append(records, record)
		prev = record.Hash
		seq++
	}
	if err := board.Validate(); err != nil {
		return nil, core.Board{}, fmt.Errorf("projected board: %w", err)
	}
	return records, board, nil
}

// commit adopts the store's returned state and replay-validated board. Callers
// hold mu.
func (w *writer) commit(result core.AppendResult) (core.State, error) {
	board := w.board
	for _, record := range result.Records {
		if err := board.Apply(record); err != nil {
			return core.State{}, fmt.Errorf("apply persisted record %s: %w", record.Type, err)
		}
	}
	if err := board.Validate(); err != nil {
		return core.State{}, fmt.Errorf("persisted board: %w", err)
	}
	if board.Revision != result.State.BoardRevision {
		return core.State{}, fmt.Errorf("persisted board revision %d does not match state %d", board.Revision, result.State.BoardRevision)
	}
	w.state = result.State
	w.board = board
	if w.publish != nil {
		for _, record := range result.Records {
			w.publish(record)
		}
	}
	return w.state, nil
}

// cloneBoard copies every slice so projecting a batch cannot mutate the
// committed board through a shared backing array.
func cloneBoard(board core.Board) core.Board {
	copy := board
	copy.Claims = append([]core.Claim(nil), board.Claims...)
	copy.Intents = append([]core.Intent(nil), board.Intents...)
	copy.Attempts = append([]core.IntentAttempt(nil), board.Attempts...)
	copy.Observations = append([]core.Observation(nil), board.Observations...)
	copy.Artifacts = append([]core.Artifact(nil), board.Artifacts...)
	return copy
}

func stateProjectionChanged(previous, next core.State) bool {
	return previous.Usage != next.Usage ||
		previous.GoalCandidate != next.GoalCandidate ||
		previous.NonGrowthRounds != next.NonGrowthRounds ||
		previous.ReasonFailures != next.ReasonFailures ||
		previous.Stagnated != next.Stagnated
}

func usageAtLeast(next, previous core.Usage) bool {
	return next.InputTokens >= previous.InputTokens &&
		next.OutputTokens >= previous.OutputTokens &&
		next.TotalTokens >= previous.TotalTokens &&
		next.ModelCalls >= previous.ModelCalls &&
		next.ToolCalls >= previous.ToolCalls &&
		next.CostUSD >= previous.CostUSD
}

func addUsage(base, delta core.Usage) core.Usage {
	// Negative deltas are clamped: transport counters must never rewind.
	nonNegative := func(value int) int {
		if value < 0 {
			return 0
		}
		return value
	}
	base.InputTokens += nonNegative(delta.InputTokens)
	base.OutputTokens += nonNegative(delta.OutputTokens)
	base.TotalTokens += nonNegative(delta.TotalTokens)
	base.ModelCalls += nonNegative(delta.ModelCalls)
	base.ToolCalls += nonNegative(delta.ToolCalls)
	if delta.CostUSD > 0 {
		base.CostUSD += delta.CostUSD
	}
	return base
}

func usageDelta(before, after core.Usage) core.Usage {
	return core.Usage{
		InputTokens:  after.InputTokens - before.InputTokens,
		OutputTokens: after.OutputTokens - before.OutputTokens,
		TotalTokens:  after.TotalTokens - before.TotalTokens,
		ModelCalls:   after.ModelCalls - before.ModelCalls,
		ToolCalls:    after.ToolCalls - before.ToolCalls,
		CostUSD:      after.CostUSD - before.CostUSD,
	}
}
