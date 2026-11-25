package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/red-copilot/harness/core"
	"github.com/red-copilot/harness/engine"
)

// errNotBridgeRun marks a Run that was not created through the v5 compatibility
// bridge (for example a v4 Run already in the database).
var errNotBridgeRun = errors.New("run is not a v5 bridge run")

// V4Bridge projects the v5 journal onto the v8 tables so the v5 kernel can run
// against the existing database generation. It is a temporary P4-P8
// compatibility layer and is deleted at the v5 cutover:
//
//   - runs.spec holds the canonical v5 Manifest;
//   - each v5 record is one events row whose raw column is the canonical record
//     envelope, preserving the per-Run hash chain;
//   - runs.metadata holds the state projection cache;
//   - claims/intents/attempts/observations/artifacts/board_commits are the v8
//     read models, rebuilt from the same records.
//
// Every write is one SQLite transaction, so the journal, state projection,
// read model and terminal guard stay atomic exactly as in the v5 store.
type V4Bridge struct {
	store *Store
}

// NewV4Bridge wraps an open v8 store. The bridge owns the store lifecycle.
func NewV4Bridge(store *Store) *V4Bridge {
	return &V4Bridge{store: store}
}

var _ engine.V5Store = (*V4Bridge)(nil)

type v4BridgeQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type v4BridgeMetadata struct {
	V5 *core.State `json:"v5_state,omitempty"`
}

// Create writes the manifest, the first journal batch and the initial
// projections in one transaction.
func (b *V4Bridge) Create(ctx context.Context, sealedManifest core.SealedManifest, first core.SealedBatch) (core.State, error) {
	manifest := sealedManifest.Manifest()
	if err := manifest.Validate(); err != nil {
		return core.State{}, err
	}
	digest, err := manifest.Digest()
	if err != nil {
		return core.State{}, err
	}
	batch := first.Batch()
	if err := batch.Validate(); err != nil {
		return core.State{}, err
	}
	if batch.RunID != manifest.RunID {
		return core.State{}, fmt.Errorf("batch run_id %q does not match manifest %q", batch.RunID, manifest.RunID)
	}
	if batch.ExpectedHead != 0 {
		return core.State{}, fmt.Errorf("create batch expected_head %d, want 0", batch.ExpectedHead)
	}
	if batch.Records[0].Type != core.RecordRunCreated {
		return core.State{}, errors.New("create batch must start with run.created")
	}
	if batch.Records[0].PrevHash != digest {
		return core.State{}, fmt.Errorf("run.created prev_hash %s does not match manifest %s", batch.Records[0].PrevHash, digest)
	}
	if payload, decodeErr := core.DecodeRecord(batch.Records[0]); decodeErr == nil {
		if created, ok := payload.(core.RunCreatedRecord); ok && created.ManifestDigest != digest {
			return core.State{}, errors.New("run.created manifest digest does not match the manifest")
		}
	}
	last := batch.Records[len(batch.Records)-1]
	state := core.State{
		RunID: manifest.RunID, Phase: core.PhaseQueued,
		HeadSeq: last.Seq, HeadHash: last.Hash, BoardRevision: batch.BoardRevision,
	}
	if batch.State != nil {
		state = *batch.State
		if state.RunID != manifest.RunID {
			return core.State{}, fmt.Errorf("state run_id %q does not match manifest %q", state.RunID, manifest.RunID)
		}
		if state.HeadSeq != 0 && state.HeadSeq != last.Seq {
			return core.State{}, fmt.Errorf("state head_seq %d does not match last record %d", state.HeadSeq, last.Seq)
		}
		if batch.BoardRevision != 0 {
			state.BoardRevision = batch.BoardRevision
		}
	}
	if state.Phase == "" {
		state.Phase = core.PhaseQueued
	}
	if state.Phase != core.PhaseQueued {
		return core.State{}, fmt.Errorf("create state phase must be queued, have %q", state.Phase)
	}
	state.HeadSeq, state.HeadHash = last.Seq, last.Hash
	if err := state.Validate(); err != nil {
		return core.State{}, err
	}
	descriptor, err := manifest.CanonicalJSON()
	if err != nil {
		return core.State{}, err
	}
	metadata, err := v4BridgeStateMetadata(state)
	if err != nil {
		return core.State{}, err
	}

	tx, err := b.store.db.BeginTx(ctx, nil)
	if err != nil {
		return core.State{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO runs(id,phase,outcome,spec,result,error,metadata,created_at,started_at,finished_at) VALUES(?,?,?,?,NULL,'',?,?,?,NULL)`,
		manifest.RunID, string(state.Phase), "", descriptor, metadata, formatTime(manifest.CreatedAt), formatTimePtr(state.StartedAt)); err != nil {
		return core.State{}, fmt.Errorf("insert bridge run: %w", err)
	}
	if err := appendV4BridgeRecordsTx(ctx, tx, batch.Records); err != nil {
		return core.State{}, err
	}
	if err := projectV4BridgeRecordsTx(ctx, tx, batch.RunID, batch.Records); err != nil {
		return core.State{}, err
	}
	if err := writeV4BoardCommitTx(ctx, tx, state); err != nil {
		return core.State{}, err
	}
	if err := tx.Commit(); err != nil {
		return core.State{}, fmt.Errorf("commit bridge create: %w", err)
	}
	return state, nil
}

// Append atomically extends the journal and updates the state projection.
func (b *V4Bridge) Append(ctx context.Context, sealed core.SealedBatch) (core.AppendResult, error) {
	batch := sealed.Batch()
	if err := batch.Validate(); err != nil {
		return core.AppendResult{}, err
	}
	tx, err := b.store.db.BeginTx(ctx, nil)
	if err != nil {
		return core.AppendResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	state, err := loadV4BridgeState(ctx, tx, batch.RunID)
	if err != nil {
		return core.AppendResult{}, err
	}
	if state.Phase == core.PhaseTerminal {
		return core.AppendResult{}, fmt.Errorf("%w: %s", core.ErrTerminal, batch.RunID)
	}
	if batch.ExpectedHead != state.HeadSeq {
		return core.AppendResult{}, fmt.Errorf("%w: expected %d, current %d", core.ErrStaleHead, batch.ExpectedHead, state.HeadSeq)
	}
	first := batch.Records[0]
	if first.Seq != state.HeadSeq+1 || first.PrevHash != state.HeadHash {
		return core.AppendResult{}, fmt.Errorf("%w: first record does not extend the current head", core.ErrStaleHead)
	}
	last := batch.Records[len(batch.Records)-1]
	next := state
	if batch.State != nil {
		next = *batch.State
		if next.RunID != state.RunID {
			return core.AppendResult{}, fmt.Errorf("state run_id %q does not match %q", next.RunID, state.RunID)
		}
		if next.Phase == core.PhaseTerminal {
			return core.AppendResult{}, errors.New("append cannot write a terminal state; use Finalize")
		}
		if next.HeadSeq != 0 && next.HeadSeq != last.Seq {
			return core.AppendResult{}, fmt.Errorf("state head_seq %d does not match last record %d", next.HeadSeq, last.Seq)
		}
		if batch.BoardRevision != 0 {
			next.BoardRevision = batch.BoardRevision
		}
	}
	next.HeadSeq, next.HeadHash = last.Seq, last.Hash
	if err := next.Validate(); err != nil {
		return core.AppendResult{}, err
	}
	metadata, err := v4BridgeStateMetadata(next)
	if err != nil {
		return core.AppendResult{}, err
	}
	if err := appendV4BridgeRecordsTx(ctx, tx, batch.Records); err != nil {
		return core.AppendResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET phase=?, outcome='', metadata=?, started_at=?, finished_at=NULL, error='' WHERE id=?`,
		string(next.Phase), metadata, formatTimePtr(next.StartedAt), next.RunID); err != nil {
		return core.AppendResult{}, fmt.Errorf("update bridge run: %w", err)
	}
	if err := projectV4BridgeRecordsTx(ctx, tx, batch.RunID, batch.Records); err != nil {
		return core.AppendResult{}, err
	}
	if err := writeV4BoardCommitTx(ctx, tx, next); err != nil {
		return core.AppendResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return core.AppendResult{}, fmt.Errorf("commit bridge append: %w", err)
	}
	return core.AppendResult{State: next, Records: batch.Records}, nil
}

// Finalize writes the terminal outcome, verdict and finalization atomically.
func (b *V4Bridge) Finalize(ctx context.Context, sealed core.SealedFinalization) (core.AppendResult, error) {
	finalization := sealed.Finalization()
	if err := finalization.Validate(); err != nil {
		return core.AppendResult{}, err
	}
	tx, err := b.store.db.BeginTx(ctx, nil)
	if err != nil {
		return core.AppendResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	state, err := loadV4BridgeState(ctx, tx, finalization.RunID)
	if err != nil {
		return core.AppendResult{}, err
	}
	if state.Phase == core.PhaseTerminal {
		return core.AppendResult{}, fmt.Errorf("%w: %s", core.ErrTerminal, finalization.RunID)
	}
	if finalization.ExpectedHead != state.HeadSeq {
		return core.AppendResult{}, fmt.Errorf("%w: expected %d, current %d", core.ErrStaleHead, finalization.ExpectedHead, state.HeadSeq)
	}
	first := finalization.Records[0]
	if first.Seq != state.HeadSeq+1 || first.PrevHash != state.HeadHash {
		return core.AppendResult{}, fmt.Errorf("%w: first final record does not extend the current head", core.ErrStaleHead)
	}
	last := finalization.Records[len(finalization.Records)-1]
	if err := validateFinalRecord(last, finalization.Outcome); err != nil {
		return core.AppendResult{}, err
	}
	if finalization.Outcome == core.OutcomeSucceeded {
		if state.Phase != core.PhaseVerifying {
			return core.AppendResult{}, fmt.Errorf("success requires verifying phase, have %q", state.Phase)
		}
		if finalization.Verdict == nil || !finalization.Verdict.Success {
			return core.AppendResult{}, errors.New("success requires a successful verdict")
		}
	}
	next := state
	next.Phase = core.PhaseTerminal
	next.Outcome = finalization.Outcome
	next.Error = finalization.Error
	next.Usage = finalization.Usage
	next.HeadSeq, next.HeadHash = last.Seq, last.Hash
	finished := finalization.FinishedAt.UTC()
	next.FinishedAt = &finished
	if err := next.Validate(); err != nil {
		return core.AppendResult{}, err
	}
	metadata, err := v4BridgeStateMetadata(next)
	if err != nil {
		return core.AppendResult{}, err
	}
	if err := appendV4BridgeRecordsTx(ctx, tx, finalization.Records); err != nil {
		return core.AppendResult{}, err
	}
	if finalization.Verdict != nil {
		authority := finalization.Verdict.Authority
		if authority == "" {
			authority = core.VerdictAuthorityVerifier
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO verdicts(run_id,authority,success,score,message,evidence,created_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET authority=excluded.authority,success=excluded.success,score=excluded.score,message=excluded.message,evidence=excluded.evidence,created_at=excluded.created_at`,
			finalization.RunID, string(authority), boolInt(finalization.Verdict.Success), finalization.Verdict.Score,
			finalization.Verdict.Message, nullableJSON(finalization.Verdict.Evidence), formatTime(finished)); err != nil {
			return core.AppendResult{}, fmt.Errorf("insert bridge verdict: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET phase=?, outcome=?, metadata=?, error=?, finished_at=? WHERE id=?`,
		string(core.PhaseTerminal), string(finalization.Outcome), metadata, finalization.Error, formatTime(finished), finalization.RunID); err != nil {
		return core.AppendResult{}, fmt.Errorf("finalize bridge run: %w", err)
	}
	if err := projectV4BridgeRecordsTx(ctx, tx, finalization.RunID, finalization.Records); err != nil {
		return core.AppendResult{}, err
	}
	if err := writeV4BoardCommitTx(ctx, tx, next); err != nil {
		return core.AppendResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return core.AppendResult{}, fmt.Errorf("commit bridge finalization: %w", err)
	}
	return core.AppendResult{State: next, Records: finalization.Records}, nil
}

// Interrupt finalizes a non-terminal Run as interrupted.
func (b *V4Bridge) Interrupt(ctx context.Context, sealed core.SealedFinalization) (core.State, error) {
	if sealed.Finalization().Outcome != core.OutcomeInterrupted {
		return core.State{}, errors.New("interrupt requires an interrupted finalization")
	}
	result, err := b.Finalize(ctx, sealed)
	if err != nil {
		return core.State{}, err
	}
	return result.State, nil
}

// AppendLogs appends redacted log records to the v8 log table.
func (b *V4Bridge) AppendLogs(ctx context.Context, runID string, logs []core.SealedLog) error {
	if len(logs) == 0 {
		return nil
	}
	tx, err := b.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var phase string
	if err := tx.QueryRowContext(ctx, `SELECT phase FROM runs WHERE id=?`, runID).Scan(&phase); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: run %s", core.ErrNotFound, runID)
		}
		return err
	}
	if phase == string(core.PhaseTerminal) {
		return fmt.Errorf("%w: %s", core.ErrTerminal, runID)
	}
	for _, sealed := range logs {
		record := sealed.Log()
		if record.RunID != runID {
			return fmt.Errorf("log run_id %q does not match %q", record.RunID, runID)
		}
		at := record.Time
		if at.IsZero() {
			at = time.Now().UTC()
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO logs(run_id,session_id,stream,time,text) VALUES(?,?,?,?,?)`,
			runID, record.SessionID, record.Stream, formatTime(at), record.Text); err != nil {
			return fmt.Errorf("append bridge log: %w", err)
		}
	}
	return tx.Commit()
}

// Manifest loads the persisted v5 manifest.
func (b *V4Bridge) Manifest(ctx context.Context, runID string) (core.Manifest, error) {
	return loadV4BridgeManifest(ctx, b.store.db, runID)
}

func loadV4BridgeManifest(ctx context.Context, q v4BridgeQuerier, runID string) (core.Manifest, error) {
	var descriptor []byte
	if err := q.QueryRowContext(ctx, `SELECT spec FROM runs WHERE id=?`, runID).Scan(&descriptor); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.Manifest{}, fmt.Errorf("%w: run %s", core.ErrNotFound, runID)
		}
		return core.Manifest{}, err
	}
	var manifest core.Manifest
	if err := json.Unmarshal(descriptor, &manifest); err != nil {
		return core.Manifest{}, fmt.Errorf("%w: decode manifest: %v", errNotBridgeRun, err)
	}
	if err := manifest.Validate(); err != nil {
		return core.Manifest{}, fmt.Errorf("%w: %v", errNotBridgeRun, err)
	}
	if manifest.RunID != runID {
		return core.Manifest{}, fmt.Errorf("%w: manifest run_id %q does not match %q", errNotBridgeRun, manifest.RunID, runID)
	}
	return manifest, nil
}

// State returns the current state projection, falling back to a journal replay
// when the cached projection is missing or does not match the journal head.
func (b *V4Bridge) State(ctx context.Context, runID string) (core.State, error) {
	return loadV4BridgeState(ctx, b.store.db, runID)
}

func loadV4BridgeState(ctx context.Context, q v4BridgeQuerier, runID string) (core.State, error) {
	manifest, err := loadV4BridgeManifest(ctx, q, runID)
	if err != nil {
		return core.State{}, err
	}
	var phase, outcome, errorText string
	var metadata []byte
	var started, finished sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT phase,outcome,metadata,error,started_at,finished_at FROM runs WHERE id=?`, runID).
		Scan(&phase, &outcome, &metadata, &errorText, &started, &finished); err != nil {
		return core.State{}, err
	}
	records, err := loadV4BridgeRecords(ctx, q, runID)
	if err != nil {
		return core.State{}, err
	}
	if len(records) == 0 {
		return core.State{}, fmt.Errorf("%w: run %s has no journal records", core.ErrCorruptJournal, runID)
	}
	last := records[len(records)-1]
	var cached v4BridgeMetadata
	if len(metadata) > 0 {
		_ = json.Unmarshal(metadata, &cached)
	}
	if cached.V5 != nil && cached.V5.HeadSeq == last.Seq && cached.V5.HeadHash == last.Hash {
		state := *cached.V5
		state.Phase, state.Outcome = core.Phase(phase), core.Outcome(outcome)
		state.Error = errorText
		state.StartedAt = parseTimePtr(started)
		state.FinishedAt = parseTimePtr(finished)
		if err := state.Validate(); err == nil {
			return state, nil
		}
	}
	return deriveV4BridgeState(ctx, q, manifest, records, phase, outcome, errorText, parseTimePtr(started), parseTimePtr(finished))
}

func deriveV4BridgeState(ctx context.Context, q v4BridgeQuerier, manifest core.Manifest, records []core.Record, phase, outcome, errorText string, started, finished *time.Time) (core.State, error) {
	digest, err := manifest.Digest()
	if err != nil {
		return core.State{}, err
	}
	if err := core.VerifyRecordChain(digest, records); err != nil {
		return core.State{}, fmt.Errorf("%w: %v", core.ErrCorruptJournal, err)
	}
	board := *core.NewBoard(manifest.RunID)
	for _, record := range records {
		if err := board.Apply(record); err != nil {
			return core.State{}, fmt.Errorf("%w: %v", core.ErrCorruptJournal, err)
		}
	}
	last := records[len(records)-1]
	state := core.State{
		RunID: manifest.RunID, Phase: core.Phase(phase), Outcome: core.Outcome(outcome),
		HeadSeq: last.Seq, HeadHash: last.Hash, BoardRevision: board.Revision,
		StartedAt: started, FinishedAt: finished, Error: errorText,
	}
	for _, record := range records {
		payload, decodeErr := core.DecodeRecord(record)
		if decodeErr != nil {
			return core.State{}, fmt.Errorf("%w: %v", core.ErrCorruptJournal, decodeErr)
		}
		switch p := payload.(type) {
		case core.RunStateRecord:
			state.Usage = p.Usage
			state.GoalCandidate = p.GoalCandidate
			state.NonGrowthRounds = p.NonGrowthRounds
			state.ReasonFailures = p.ReasonFailures
			state.Stagnated = p.Stagnated
		case core.RunGoalCandidateRecord:
			state.GoalCandidate = true
		case core.RunFinalizedRecord:
			state.Usage = p.Usage
		}
	}
	if err := state.Validate(); err != nil {
		return core.State{}, fmt.Errorf("%w: derived state: %v", core.ErrCorruptJournal, err)
	}
	return state, nil
}

// Events returns journal records after the supplied per-Run sequence.
func (b *V4Bridge) Events(ctx context.Context, runID string, after uint64) ([]core.Record, error) {
	records, err := loadV4BridgeRecords(ctx, b.store.db, runID)
	if err != nil {
		return nil, err
	}
	out := make([]core.Record, 0, len(records))
	for _, record := range records {
		if record.Seq > after {
			out = append(out, record)
		}
	}
	return out, nil
}

// Logs returns redacted log records after the supplied log sequence.
func (b *V4Bridge) Logs(ctx context.Context, runID string, after uint64) ([]core.LogRecord, error) {
	return b.store.ListLogs(ctx, runID, int64(after))
}

// Verdict returns the persisted verdict, if any.
func (b *V4Bridge) Verdict(ctx context.Context, runID string) (*core.Verdict, error) {
	record, err := b.store.GetVerdict(ctx, runID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &core.Verdict{Authority: record.Authority, Success: record.Success, Score: record.Score, Message: record.Message, Evidence: record.Evidence}, nil
}

// Board reconstructs the board from the v8 read model and validates it.
func (b *V4Bridge) Board(ctx context.Context, runID string) (core.Board, error) {
	state, err := b.State(ctx, runID)
	if err != nil {
		return core.Board{}, err
	}
	claims, err := b.store.ListClaims(ctx, runID)
	if err != nil {
		return core.Board{}, err
	}
	intents, err := b.store.ListIntents(ctx, runID)
	if err != nil {
		return core.Board{}, err
	}
	attempts, err := b.store.ListIntentAttempts(ctx, runID)
	if err != nil {
		return core.Board{}, err
	}
	observations, err := b.store.ListBlackboardObservations(ctx, runID)
	if err != nil {
		return core.Board{}, err
	}
	artifacts, err := b.store.ListArtifacts(ctx, runID)
	if err != nil {
		return core.Board{}, err
	}
	revision, err := boardRevision(ctx, b.store.db, runID)
	if err != nil {
		return core.Board{}, err
	}
	if revision != state.BoardRevision {
		return core.Board{}, fmt.Errorf("%w: read model revision %d does not match state %d", core.ErrCorruptJournal, revision, state.BoardRevision)
	}
	board := core.Board{RunID: runID, Revision: revision, Claims: claims, Intents: intents, Attempts: attempts, Observations: observations, Artifacts: artifacts}
	if err := board.Validate(); err != nil {
		return core.Board{}, fmt.Errorf("%w: %v", core.ErrCorruptJournal, err)
	}
	return board, nil
}

// ListArtifacts returns the artifact read model.
func (b *V4Bridge) ListArtifacts(ctx context.Context, runID string) ([]core.Artifact, error) {
	return b.store.ListArtifacts(ctx, runID)
}

// OpenArtifact reads one content-addressed blob.
func (b *V4Bridge) OpenArtifact(ctx context.Context, sha256 string) (io.ReadCloser, error) {
	return b.store.OpenArtifact(ctx, sha256)
}

// VerifyChain verifies the per-Run journal chain and its state head.
func (b *V4Bridge) VerifyChain(ctx context.Context, runID string) error {
	manifest, err := b.Manifest(ctx, runID)
	if err != nil {
		return err
	}
	digest, err := manifest.Digest()
	if err != nil {
		return err
	}
	records, err := loadV4BridgeRecords(ctx, b.store.db, runID)
	if err != nil {
		return err
	}
	if err := core.VerifyRecordChain(digest, records); err != nil {
		return fmt.Errorf("%w: %v", core.ErrCorruptJournal, err)
	}
	state, err := b.State(ctx, runID)
	if err != nil {
		return err
	}
	if uint64(len(records)) != state.HeadSeq {
		return fmt.Errorf("%w: journal has %d records, state head is %d", core.ErrCorruptJournal, len(records), state.HeadSeq)
	}
	if records[len(records)-1].Hash != state.HeadHash {
		return fmt.Errorf("%w: journal head does not match state head", core.ErrCorruptJournal)
	}
	return nil
}

// NonTerminal lists non-terminal bridge Runs.
func (b *V4Bridge) NonTerminal(ctx context.Context) ([]core.State, error) {
	rows, err := b.store.db.QueryContext(ctx, `SELECT id FROM runs WHERE phase <> ? ORDER BY created_at`, string(core.PhaseTerminal))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []core.State
	for _, id := range ids {
		state, err := b.State(ctx, id)
		if err != nil {
			if errors.Is(err, errNotBridgeRun) || errors.Is(err, core.ErrNotFound) {
				continue
			}
			return nil, err
		}
		out = append(out, state)
	}
	return out, nil
}

// Replay reconstructs a Run from its journal and validates every invariant.
func (b *V4Bridge) Replay(ctx context.Context, runID string) (core.Restored, error) {
	manifest, err := b.Manifest(ctx, runID)
	if err != nil {
		return core.Restored{}, err
	}
	digest, err := manifest.Digest()
	if err != nil {
		return core.Restored{}, err
	}
	records, err := loadV4BridgeRecords(ctx, b.store.db, runID)
	if err != nil {
		return core.Restored{}, err
	}
	if err := core.VerifyRecordChain(digest, records); err != nil {
		return core.Restored{}, fmt.Errorf("%w: %v", core.ErrCorruptJournal, err)
	}
	board := *core.NewBoard(runID)
	for _, record := range records {
		if err := board.Apply(record); err != nil {
			return core.Restored{}, fmt.Errorf("%w: %v", core.ErrCorruptJournal, err)
		}
	}
	state, err := loadV4BridgeState(ctx, b.store.db, runID)
	if err != nil {
		return core.Restored{}, err
	}
	if err := board.Validate(); err != nil {
		return core.Restored{}, fmt.Errorf("%w: %v", core.ErrCorruptJournal, err)
	}
	if board.Revision != state.BoardRevision {
		return core.Restored{}, fmt.Errorf("%w: replayed board revision %d does not match state %d", core.ErrCorruptJournal, board.Revision, state.BoardRevision)
	}
	return core.Restored{Manifest: manifest, State: state, Board: board, HeadSeq: state.HeadSeq, HeadHash: state.HeadHash}, nil
}

// Put stores already-sealed evidence through the v8 blob table.
func (b *V4Bridge) Put(ctx context.Context, artifact core.SealedArtifact) (core.Artifact, error) {
	return b.store.Put(ctx, artifact.Artifact())
}

// Close closes the wrapped v8 store.
func (b *V4Bridge) Close() error { return b.store.Close() }

// loadV4BridgeRecords decodes the canonical record envelopes stored in the v8
// events table. Rows without an envelope are not bridge records.
func loadV4BridgeRecords(ctx context.Context, q v4BridgeQuerier, runID string) ([]core.Record, error) {
	rows, err := q.QueryContext(ctx, `SELECT raw FROM events WHERE run_id=? ORDER BY seq`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Record
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if len(raw) == 0 {
			return nil, fmt.Errorf("%w: run %s has an event without a v5 record envelope", core.ErrCorruptJournal, runID)
		}
		var record core.Record
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, fmt.Errorf("%w: decode record: %v", core.ErrCorruptJournal, err)
		}
		if record.RunID != runID {
			return nil, fmt.Errorf("%w: record run_id %q does not match %q", core.ErrCorruptJournal, record.RunID, runID)
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// appendV4BridgeRecordsTx writes one v8 events row per v5 record. The raw
// column keeps the exact record envelope, including the per-Run hash chain.
func appendV4BridgeRecordsTx(ctx context.Context, tx *sql.Tx, records []core.Record) error {
	for _, record := range records {
		raw, err := core.CanonicalJSON(record)
		if err != nil {
			return err
		}
		event := core.Event{
			RunID: record.RunID, Source: "v5", Type: core.EventType(record.Type),
			Time: record.At.UTC(), Data: record.Payload, Raw: raw, SchemaVersion: record.SchemaVersion,
		}
		if _, err := appendHashedEventTx(ctx, tx, event); err != nil {
			return fmt.Errorf("append bridge record %s seq %d: %w", record.Type, record.Seq, err)
		}
	}
	return nil
}

// projectV4BridgeRecordsTx mirrors the v5 read model projection into the v8
// normalized tables so the existing CLI read commands keep working.
func projectV4BridgeRecordsTx(ctx context.Context, tx *sql.Tx, runID string, records []core.Record) error {
	for _, record := range records {
		if record.RunID != runID {
			return fmt.Errorf("record run_id %q does not match %q", record.RunID, runID)
		}
		payload, err := core.DecodeRecord(record)
		if err != nil {
			return err
		}
		switch p := payload.(type) {
		case core.ClaimCreatedRecord:
			if err := saveClaimTx(ctx, tx, p.Claim); err != nil {
				return err
			}
		case core.IntentProposedRecord:
			if err := saveIntentTx(ctx, tx, p.Intent); err != nil {
				return err
			}
		case core.IntentClaimedRecord:
			intent, err := loadV4IntentTx(ctx, tx, runID, p.IntentID)
			if err != nil {
				return err
			}
			at := p.At.UTC()
			intent.State = core.IntentClaimed
			intent.ExecutorID = p.ExecutorID
			intent.ClaimedAt = &at
			if err := saveIntentTx(ctx, tx, intent); err != nil {
				return err
			}
		case core.IntentConcludedRecord:
			intent, err := loadV4IntentTx(ctx, tx, runID, p.IntentID)
			if err != nil {
				return err
			}
			at := p.At.UTC()
			intent.State = core.IntentConcluded
			intent.ResultClaimID = p.ResultClaimID
			intent.ConcludedAt = &at
			if err := saveIntentTx(ctx, tx, intent); err != nil {
				return err
			}
		case core.IntentAbandonedRecord:
			intent, err := loadV4IntentTx(ctx, tx, runID, p.IntentID)
			if err != nil {
				return err
			}
			at := p.At.UTC()
			intent.State = core.IntentAbandoned
			intent.Error = p.Reason
			intent.ConcludedAt = &at
			if err := saveIntentTx(ctx, tx, intent); err != nil {
				return err
			}
		case core.AttemptStartedRecord:
			if err := saveAttemptTx(ctx, tx, p.Attempt); err != nil {
				return err
			}
		case core.AttemptConcludedRecord:
			if err := updateV4AttemptTx(ctx, tx, runID, p.AttemptID, core.IntentAttemptConcluded, p.Conclusion, "", p.At); err != nil {
				return err
			}
		case core.AttemptFailedRecord:
			if err := updateV4AttemptTx(ctx, tx, runID, p.AttemptID, core.IntentAttemptFailed, "", p.Error, p.At); err != nil {
				return err
			}
		case core.AttemptCancelledRecord:
			if err := updateV4AttemptTx(ctx, tx, runID, p.AttemptID, core.IntentAttemptCancelled, "", "", p.At); err != nil {
				return err
			}
		case core.AttemptTimedOutRecord:
			if err := updateV4AttemptTx(ctx, tx, runID, p.AttemptID, core.IntentAttemptTimedOut, "", "", p.At); err != nil {
				return err
			}
		case core.ObservationCreatedRecord:
			if err := saveObservationTx(ctx, tx, p.Observation); err != nil {
				return err
			}
		case core.ArtifactStoredRecord:
			if err := saveArtifactTx(ctx, tx, p.Artifact); err != nil {
				return err
			}
		case core.RunResumedRecord:
			if err := normalizeV4ProjectionTx(ctx, tx, runID, p.Normalizations, record.At); err != nil {
				return err
			}
		}
	}
	return nil
}

func normalizeV4ProjectionTx(ctx context.Context, tx *sql.Tx, runID string, normalizations []string, at time.Time) error {
	for _, normalization := range normalizations {
		switch normalization {
		case core.NormalizeAttemptsInterrupted:
			attempts, err := listV4AttemptsTx(ctx, tx, runID)
			if err != nil {
				return err
			}
			for _, attempt := range attempts {
				if attempt.Phase != core.IntentAttemptRunning {
					continue
				}
				attempt.Phase = core.IntentAttemptInterrupted
				if attempt.Error == "" {
					attempt.Error = "interrupted by resume"
				}
				attempt.FinishedAt = at.UTC()
				if err := saveAttemptTx(ctx, tx, attempt); err != nil {
					return err
				}
			}
		case core.NormalizeIntentsReopened:
			intents, err := listV4IntentsTx(ctx, tx, runID)
			if err != nil {
				return err
			}
			for _, intent := range intents {
				if intent.State != core.IntentClaimed {
					continue
				}
				intent.State = core.IntentOpen
				intent.ExecutorID = ""
				intent.ClaimedAt = nil
				if err := saveIntentTx(ctx, tx, intent); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unknown resume normalization %q", normalization)
		}
	}
	return nil
}

func loadV4IntentTx(ctx context.Context, tx *sql.Tx, runID, intentID string) (core.Intent, error) {
	var payload []byte
	if err := tx.QueryRowContext(ctx, `SELECT payload FROM intents WHERE run_id=? AND id=?`, runID, intentID).Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.Intent{}, fmt.Errorf("%w: intent %s", core.ErrNotFound, intentID)
		}
		return core.Intent{}, err
	}
	var intent core.Intent
	if err := json.Unmarshal(payload, &intent); err != nil {
		return core.Intent{}, err
	}
	return intent, nil
}

func listV4IntentsTx(ctx context.Context, tx *sql.Tx, runID string) ([]core.Intent, error) {
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM intents WHERE run_id=?`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Intent
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var intent core.Intent
		if err := json.Unmarshal(payload, &intent); err != nil {
			return nil, err
		}
		out = append(out, intent)
	}
	return out, rows.Err()
}

func loadV4AttemptTx(ctx context.Context, tx *sql.Tx, runID, attemptID string) (core.IntentAttempt, error) {
	var payload []byte
	if err := tx.QueryRowContext(ctx, `SELECT payload FROM attempts WHERE run_id=? AND id=?`, runID, attemptID).Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.IntentAttempt{}, fmt.Errorf("%w: attempt %s", core.ErrNotFound, attemptID)
		}
		return core.IntentAttempt{}, err
	}
	var attempt core.IntentAttempt
	if err := json.Unmarshal(payload, &attempt); err != nil {
		return core.IntentAttempt{}, err
	}
	return attempt, nil
}

func listV4AttemptsTx(ctx context.Context, tx *sql.Tx, runID string) ([]core.IntentAttempt, error) {
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM attempts WHERE run_id=?`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.IntentAttempt
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var attempt core.IntentAttempt
		if err := json.Unmarshal(payload, &attempt); err != nil {
			return nil, err
		}
		out = append(out, attempt)
	}
	return out, rows.Err()
}

func updateV4AttemptTx(ctx context.Context, tx *sql.Tx, runID, attemptID string, phase core.IntentAttemptPhase, conclusion, failure string, at time.Time) error {
	attempt, err := loadV4AttemptTx(ctx, tx, runID, attemptID)
	if err != nil {
		return err
	}
	attempt.Phase = phase
	attempt.Conclusion = conclusion
	attempt.Error = failure
	attempt.FinishedAt = at.UTC()
	return saveAttemptTx(ctx, tx, attempt)
}

func writeV4BoardCommitTx(ctx context.Context, tx *sql.Tx, state core.State) error {
	if state.BoardRevision == 0 {
		return nil
	}
	var current uint64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision),0) FROM board_commits WHERE run_id=?`, state.RunID).Scan(&current); err != nil {
		return err
	}
	if state.BoardRevision <= current {
		return nil
	}
	payload, err := core.CanonicalJSON(struct {
		RunID    string `json:"run_id"`
		Revision uint64 `json:"revision"`
	}{RunID: state.RunID, Revision: state.BoardRevision})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO board_commits(run_id,revision,payload,created_at) VALUES(?,?,?,?)`,
		state.RunID, state.BoardRevision, payload, formatTime(time.Now().UTC()))
	return err
}

func v4BridgeStateMetadata(state core.State) ([]byte, error) {
	return core.CanonicalJSON(v4BridgeMetadata{V5: &state})
}
