package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/red-copilot/harness/core"
)

// V5AppendResult is the state and records produced by one journal write.
//
// Deprecated: use core.AppendResult.
type V5AppendResult = core.AppendResult

// Create writes the manifest, the first journal batch, the initial state, the
// read-model projection and the first checkpoint in one transaction.
func (s *V5Store) Create(ctx context.Context, sealedManifest core.SealedManifest, first core.SealedBatch) (core.State, error) {
	if s.readOnly {
		return core.State{}, errors.New("read-only v5 store")
	}
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
	last := batch.Records[len(batch.Records)-1]
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

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.State{}, err
	}
	defer func() { _ = tx.Rollback() }()
	descriptor, err := manifest.CanonicalJSON()
	if err != nil {
		return core.State{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO run_manifests(run_id,descriptor,descriptor_sha256,created_at) VALUES(?,?,?,?)`,
		manifest.RunID, descriptor, digest, manifest.CreatedAt.UTC().UnixNano()); err != nil {
		return core.State{}, fmt.Errorf("insert manifest: %w", err)
	}
	if err := insertRecordsTx(ctx, tx, batch.Records); err != nil {
		return core.State{}, err
	}
	if s.testAfterJournal != nil {
		if err := s.testAfterJournal(); err != nil {
			return core.State{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO run_state(run_id,phase,outcome,head_seq,head_hash,board_revision,usage,goal_candidate,non_growth_rounds,reason_failures,stagnated,started_at,finished_at,error,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		state.RunID, string(state.Phase), string(state.Outcome), state.HeadSeq, state.HeadHash, state.BoardRevision,
		usageJSON(state.Usage), boolInt(state.GoalCandidate), state.NonGrowthRounds, state.ReasonFailures,
		boolInt(state.Stagnated), timePtrNanos(state.StartedAt), timePtrNanos(state.FinishedAt), state.Error, time.Now().UnixNano()); err != nil {
		return core.State{}, fmt.Errorf("insert run state: %w", err)
	}
	if err := projectRecordsTx(ctx, tx, batch.RunID, batch.Records); err != nil {
		return core.State{}, err
	}
	board, err := loadBoardTx(ctx, tx, batch.RunID, state.BoardRevision)
	if err != nil {
		return core.State{}, err
	}
	if err := writeCheckpointTx(ctx, tx, state, board); err != nil {
		return core.State{}, err
	}
	if err := tx.Commit(); err != nil {
		return core.State{}, fmt.Errorf("commit run create: %w", err)
	}
	return state, nil
}

// Append atomically extends the journal and updates the state projection.
func (s *V5Store) Append(ctx context.Context, sealed core.SealedBatch) (core.AppendResult, error) {
	if s.readOnly {
		return core.AppendResult{}, errors.New("read-only v5 store")
	}
	batch := sealed.Batch()
	if err := batch.Validate(); err != nil {
		return core.AppendResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.AppendResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	state, err := loadStateTx(ctx, tx, batch.RunID)
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
	if err := insertRecordsTx(ctx, tx, batch.Records); err != nil {
		return core.AppendResult{}, err
	}
	if s.testAfterJournal != nil {
		if err := s.testAfterJournal(); err != nil {
			return core.AppendResult{}, err
		}
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
	updateStateTx(ctx, tx, next)
	if err := projectRecordsTx(ctx, tx, batch.RunID, batch.Records); err != nil {
		return core.AppendResult{}, err
	}
	if next.Phase != state.Phase || last.Seq%256 == 0 {
		board, err := loadBoardTx(ctx, tx, batch.RunID, next.BoardRevision)
		if err != nil {
			return core.AppendResult{}, err
		}
		if err := writeCheckpointTx(ctx, tx, next, board); err != nil {
			return core.AppendResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return core.AppendResult{}, fmt.Errorf("commit append: %w", err)
	}
	return core.AppendResult{State: next, Records: batch.Records}, nil
}

// Finalize writes the terminal outcome, verdict and finalization atomically.
func (s *V5Store) Finalize(ctx context.Context, sealed core.SealedFinalization) (core.AppendResult, error) {
	if s.readOnly {
		return core.AppendResult{}, errors.New("read-only v5 store")
	}
	finalization := sealed.Finalization()
	if err := finalization.Validate(); err != nil {
		return core.AppendResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.AppendResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	state, err := loadStateTx(ctx, tx, finalization.RunID)
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
	manifest, _, err := loadManifestTx(ctx, tx, finalization.RunID)
	if err != nil {
		return core.AppendResult{}, err
	}
	if err := insertRecordsTx(ctx, tx, finalization.Records); err != nil {
		return core.AppendResult{}, err
	}
	if s.testAfterJournal != nil {
		if err := s.testAfterJournal(); err != nil {
			return core.AppendResult{}, err
		}
	}
	verdictSHA := ""
	if finalization.Verdict != nil {
		data, err := core.CanonicalJSON(finalization.Verdict)
		if err != nil {
			return core.AppendResult{}, err
		}
		sum := sha256.Sum256(data)
		verdictSHA = hex.EncodeToString(sum[:])
		if _, err := tx.ExecContext(ctx, `INSERT INTO verdicts(run_id,success,score,message,evidence,verifier_id,created_at) VALUES(?,?,?,?,?,?,?)`,
			finalization.RunID, boolInt(finalization.Verdict.Success), finalization.Verdict.Score,
			finalization.Verdict.Message, nullableBytes(finalization.Verdict.Evidence),
			manifest.Verifier.ID, finalization.FinishedAt.UTC().UnixNano()); err != nil {
			return core.AppendResult{}, fmt.Errorf("insert verdict: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO run_finalization(run_id,outcome,journal_seq,verdict_sha256,finalized_at) VALUES(?,?,?,?,?)`,
		finalization.RunID, string(finalization.Outcome), last.Seq, nullableString(verdictSHA), finalization.FinishedAt.UTC().UnixNano()); err != nil {
		return core.AppendResult{}, fmt.Errorf("insert finalization: %w", err)
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
	updateStateTx(ctx, tx, next)
	if err := projectRecordsTx(ctx, tx, finalization.RunID, finalization.Records); err != nil {
		return core.AppendResult{}, err
	}
	board, err := loadBoardTx(ctx, tx, finalization.RunID, next.BoardRevision)
	if err != nil {
		return core.AppendResult{}, err
	}
	if err := writeCheckpointTx(ctx, tx, next, board); err != nil {
		return core.AppendResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return core.AppendResult{}, fmt.Errorf("commit finalization: %w", err)
	}
	return core.AppendResult{State: next, Records: finalization.Records}, nil
}

// Interrupt finalizes a non-terminal Run as interrupted. The caller supplies
// the sealed terminal write; the store checks the outcome and head.
func (s *V5Store) Interrupt(ctx context.Context, sealed core.SealedFinalization) (core.State, error) {
	finalization := sealed.Finalization()
	if finalization.Outcome != core.OutcomeInterrupted {
		return core.State{}, errors.New("interrupt requires an interrupted finalization")
	}
	result, err := s.Finalize(ctx, sealed)
	if err != nil {
		return core.State{}, err
	}
	return result.State, nil
}

// AppendLogs appends redacted log records after the run's current log head.
func (s *V5Store) AppendLogs(ctx context.Context, runID string, logs []core.SealedLog) error {
	if s.readOnly {
		return errors.New("read-only v5 store")
	}
	if len(logs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	state, err := loadStateTx(ctx, tx, runID)
	if err != nil {
		return err
	}
	if state.Phase == core.PhaseTerminal {
		return fmt.Errorf("%w: %s", core.ErrTerminal, runID)
	}
	var base uint64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM run_logs WHERE run_id=?`, runID).Scan(&base); err != nil {
		return err
	}
	for i, sealed := range logs {
		record := sealed.Log()
		if record.RunID != runID {
			return fmt.Errorf("log run_id %q does not match %q", record.RunID, runID)
		}
		at := record.Time
		if at.IsZero() {
			at = time.Now().UTC()
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO run_logs(run_id,seq,session_id,stream,at,text) VALUES(?,?,?,?,?,?)`,
			runID, base+1+uint64(i), record.SessionID, record.Stream, at.UTC().UnixNano(), record.Text); err != nil {
			return fmt.Errorf("insert log: %w", err)
		}
	}
	return tx.Commit()
}

func validateFinalRecord(record core.Record, outcome core.Outcome) error {
	payload, err := core.DecodeRecord(record)
	if err != nil {
		return err
	}
	switch p := payload.(type) {
	case core.RunFinalizedRecord:
		if p.Outcome != outcome {
			return fmt.Errorf("run.finalized outcome %q does not match %q", p.Outcome, outcome)
		}
	case core.RunInterruptedRecord:
		if outcome != core.OutcomeInterrupted {
			return errors.New("run.interrupted requires an interrupted outcome")
		}
	default:
		return fmt.Errorf("final record type %q is not a terminal record", record.Type)
	}
	return nil
}

func loadStateTx(ctx context.Context, tx *sql.Tx, runID string) (core.State, error) {
	state, err := scanStateRow(tx.QueryRowContext(ctx, stateSelect, runID), runID)
	if errors.Is(err, sql.ErrNoRows) {
		return core.State{}, fmt.Errorf("%w: run %s", core.ErrNotFound, runID)
	}
	return state, err
}

func updateStateTx(ctx context.Context, tx *sql.Tx, state core.State) error {
	_, err := tx.ExecContext(ctx, `UPDATE run_state SET phase=?,outcome=?,head_seq=?,head_hash=?,board_revision=?,usage=?,goal_candidate=?,non_growth_rounds=?,reason_failures=?,stagnated=?,started_at=?,finished_at=?,error=?,updated_at=? WHERE run_id=?`,
		string(state.Phase), string(state.Outcome), state.HeadSeq, state.HeadHash, state.BoardRevision,
		usageJSON(state.Usage), boolInt(state.GoalCandidate), state.NonGrowthRounds, state.ReasonFailures,
		boolInt(state.Stagnated), timePtrNanos(state.StartedAt), timePtrNanos(state.FinishedAt), state.Error, time.Now().UnixNano(), state.RunID)
	if err != nil {
		return fmt.Errorf("update run state: %w", err)
	}
	return nil
}

func insertRecordsTx(ctx context.Context, tx *sql.Tx, records []core.Record) error {
	for _, record := range records {
		if _, err := tx.ExecContext(ctx, `INSERT INTO run_journal(run_id,seq,type,schema_version,at,payload,prev_hash,hash) VALUES(?,?,?,?,?,?,?,?)`,
			record.RunID, record.Seq, string(record.Type), record.SchemaVersion, record.At.UTC().UnixNano(), []byte(record.Payload), record.PrevHash, record.Hash); err != nil {
			return fmt.Errorf("insert journal seq %d: %w", record.Seq, err)
		}
	}
	return nil
}

func loadManifestTx(ctx context.Context, tx *sql.Tx, runID string) (core.Manifest, string, error) {
	var descriptor []byte
	var digest string
	err := tx.QueryRowContext(ctx, `SELECT descriptor,descriptor_sha256 FROM run_manifests WHERE run_id=?`, runID).Scan(&descriptor, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return core.Manifest{}, "", fmt.Errorf("%w: manifest %s", core.ErrNotFound, runID)
	}
	if err != nil {
		return core.Manifest{}, "", fmt.Errorf("load manifest: %w", err)
	}
	var manifest core.Manifest
	if err := json.Unmarshal(descriptor, &manifest); err != nil {
		return core.Manifest{}, "", fmt.Errorf("decode manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return core.Manifest{}, "", err
	}
	computed, err := manifest.Digest()
	if err != nil {
		return core.Manifest{}, "", err
	}
	if computed != digest {
		return core.Manifest{}, "", fmt.Errorf("%w: manifest digest %s does not match %s", core.ErrCorruptJournal, digest, computed)
	}
	return manifest, digest, nil
}

func writeCheckpointTx(ctx context.Context, tx *sql.Tx, state core.State, board core.Board) error {
	checkpoint := core.Checkpoint{SchemaVersion: core.CheckpointSchemaVersion, State: state, Board: board}
	data, err := checkpoint.CanonicalJSON()
	if err != nil {
		return err
	}
	digest, err := checkpoint.Digest()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO run_checkpoints(run_id,seq,state_version,state,state_sha256,head_hash,created_at) VALUES(?,?,?,?,?,?,?)`,
		state.RunID, state.HeadSeq, checkpoint.SchemaVersion, data, digest, state.HeadHash, time.Now().UnixNano())
	if err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}
	return nil
}

func usageJSON(usage core.Usage) []byte {
	data, _ := json.Marshal(usage)
	return data
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func timePtrNanos(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC().UnixNano()
}
