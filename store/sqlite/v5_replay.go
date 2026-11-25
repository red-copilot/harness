package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/red-copilot/harness/core"
)

// Replay reconstructs a Run from the newest valid checkpoint plus journal tail,
// or from genesis when no checkpoint is usable. Any inconsistency fails closed.
func (s *V5Store) Replay(ctx context.Context, runID string) (core.Restored, error) {
	manifest, digest, err := s.manifestDigest(ctx, runID)
	if err != nil {
		return core.Restored{}, err
	}
	state, err := s.State(ctx, runID)
	if err != nil {
		return core.Restored{}, err
	}
	board := *core.NewBoard(runID)
	prev := digest
	var from uint64 = 1
	if checkpoint, cpSeq, ok, err := s.loadCheckpoint(ctx, runID, state.HeadSeq); err != nil {
		return core.Restored{}, err
	} else if ok {
		board = checkpoint.Board
		prev = checkpoint.State.HeadHash
		from = cpSeq + 1
	}

	var records []core.Record
	if from <= state.HeadSeq {
		records, err = s.Events(ctx, runID, from-1)
		if err != nil {
			return core.Restored{}, err
		}
	}
	expected := state.HeadSeq - (from - 1)
	if uint64(len(records)) != expected {
		return core.Restored{}, fmt.Errorf("%w: replay has %d records, want %d", core.ErrCorruptJournal, len(records), expected)
	}
	for i, record := range records {
		wantSeq := from + uint64(i)
		if record.Seq != wantSeq {
			return core.Restored{}, fmt.Errorf("%w: replay record seq %d, want %d", core.ErrCorruptJournal, record.Seq, wantSeq)
		}
		if record.PrevHash != prev {
			return core.Restored{}, fmt.Errorf("%w: record seq %d does not chain to %s", core.ErrCorruptJournal, record.Seq, prev)
		}
		hash, err := core.ChainHash(prev, record)
		if err != nil {
			return core.Restored{}, fmt.Errorf("%w: %v", core.ErrCorruptJournal, err)
		}
		if hash != record.Hash {
			return core.Restored{}, fmt.Errorf("%w: record seq %d hash mismatch", core.ErrCorruptJournal, record.Seq)
		}
		if err := board.Apply(record); err != nil {
			return core.Restored{}, fmt.Errorf("%w: %v", core.ErrCorruptJournal, err)
		}
		prev = record.Hash
	}
	if prev != state.HeadHash {
		return core.Restored{}, fmt.Errorf("%w: replay head %s does not match state %s", core.ErrCorruptJournal, prev, state.HeadHash)
	}
	if err := board.Validate(); err != nil {
		return core.Restored{}, fmt.Errorf("%w: %v", core.ErrCorruptJournal, err)
	}
	if board.Revision != state.BoardRevision {
		return core.Restored{}, fmt.Errorf("%w: replayed board revision %d does not match state %d", core.ErrCorruptJournal, board.Revision, state.BoardRevision)
	}
	board.Revision = state.BoardRevision
	return core.Restored{Manifest: manifest, State: state, Board: board, HeadSeq: state.HeadSeq, HeadHash: state.HeadHash}, nil
}

func (s *V5Store) loadCheckpoint(ctx context.Context, runID string, headSeq uint64) (core.Checkpoint, uint64, bool, error) {
	var seq uint64
	var data []byte
	var stateSHA, headHash string
	err := s.db.QueryRowContext(ctx, `SELECT seq,state,state_sha256,head_hash FROM run_checkpoints WHERE run_id=? AND seq<=? ORDER BY seq DESC LIMIT 1`, runID, headSeq).
		Scan(&seq, &data, &stateSHA, &headHash)
	if errors.Is(err, sql.ErrNoRows) {
		return core.Checkpoint{}, 0, false, nil
	}
	if err != nil {
		return core.Checkpoint{}, 0, false, err
	}
	var checkpoint core.Checkpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return core.Checkpoint{}, 0, false, nil
	}
	if err := checkpoint.Validate(); err != nil {
		return core.Checkpoint{}, 0, false, nil
	}
	if checkpoint.State.RunID != runID || checkpoint.State.HeadSeq != seq {
		return core.Checkpoint{}, 0, false, nil
	}
	computed, err := checkpoint.Digest()
	if err != nil || computed != stateSHA || checkpoint.State.HeadHash != headHash {
		return core.Checkpoint{}, 0, false, nil
	}
	var journalHash string
	if err := s.db.QueryRowContext(ctx, `SELECT hash FROM run_journal WHERE run_id=? AND seq=?`, runID, seq).Scan(&journalHash); err != nil {
		return core.Checkpoint{}, 0, false, nil
	}
	if journalHash != checkpoint.State.HeadHash {
		return core.Checkpoint{}, 0, false, nil
	}
	return checkpoint, seq, true, nil
}

// RebuildReadModel deletes and deterministically rebuilds the run's rm_* rows
// from the journal.
func (s *V5Store) RebuildReadModel(ctx context.Context, runID string) error {
	if s.readOnly {
		return errors.New("read-only v5 store")
	}
	_, digest, err := s.manifestDigest(ctx, runID)
	if err != nil {
		return err
	}
	records, err := s.Events(ctx, runID, 0)
	if err != nil {
		return err
	}
	if err := core.VerifyRecordChain(digest, records); err != nil {
		return fmt.Errorf("%w: %v", core.ErrCorruptJournal, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, table := range []string{"rm_claims", "rm_intents", "rm_attempts", "rm_observations", "rm_artifacts"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE run_id=?`, runID); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	if err := projectRecordsTx(ctx, tx, runID, records); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}
