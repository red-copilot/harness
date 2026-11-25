package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/red-copilot/harness/core"
)

const stateColumns = `run_id,phase,outcome,head_seq,head_hash,board_revision,usage,goal_candidate,non_growth_rounds,reason_failures,stagnated,started_at,finished_at,error`
const stateSelect = `SELECT ` + stateColumns + ` FROM run_state WHERE run_id=?`

type v5Queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// Manifest loads and validates the persisted manifest.
func (s *V5Store) Manifest(ctx context.Context, runID string) (core.Manifest, error) {
	var descriptor []byte
	var digest string
	err := s.db.QueryRowContext(ctx, `SELECT descriptor,descriptor_sha256 FROM run_manifests WHERE run_id=?`, runID).Scan(&descriptor, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return core.Manifest{}, fmt.Errorf("%w: manifest %s", core.ErrNotFound, runID)
	}
	if err != nil {
		return core.Manifest{}, fmt.Errorf("load manifest: %w", err)
	}
	var manifest core.Manifest
	if err := json.Unmarshal(descriptor, &manifest); err != nil {
		return core.Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return core.Manifest{}, err
	}
	computed, err := manifest.Digest()
	if err != nil {
		return core.Manifest{}, err
	}
	if computed != digest {
		return core.Manifest{}, fmt.Errorf("%w: manifest digest %s does not match %s", core.ErrCorruptJournal, digest, computed)
	}
	return manifest, nil
}

// State loads the current run state projection.
func (s *V5Store) State(ctx context.Context, runID string) (core.State, error) {
	state, err := scanStateRow(s.db.QueryRowContext(ctx, stateSelect, runID), runID)
	if errors.Is(err, sql.ErrNoRows) {
		return core.State{}, fmt.Errorf("%w: run %s", core.ErrNotFound, runID)
	}
	return state, err
}

// Events returns journal records after seq.
func (s *V5Store) Events(ctx context.Context, runID string, after uint64) ([]core.Record, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT run_id,seq,type,schema_version,at,payload,prev_hash,hash FROM run_journal WHERE run_id=? AND seq>? ORDER BY seq`, runID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Record
	for rows.Next() {
		var record core.Record
		var typ string
		var at int64
		var payload []byte
		if err := rows.Scan(&record.RunID, &record.Seq, &typ, &record.SchemaVersion, &at, &payload, &record.PrevHash, &record.Hash); err != nil {
			return nil, err
		}
		record.Type = core.RecordType(typ)
		record.At = time.Unix(0, at).UTC()
		record.Payload = payload
		out = append(out, record)
	}
	return out, rows.Err()
}

// Logs returns log records after seq.
func (s *V5Store) Logs(ctx context.Context, runID string, after uint64) ([]core.LogRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq,run_id,session_id,stream,at,text FROM run_logs WHERE run_id=? AND seq>? ORDER BY seq`, runID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.LogRecord
	for rows.Next() {
		var record core.LogRecord
		var at int64
		if err := rows.Scan(&record.Seq, &record.RunID, &record.SessionID, &record.Stream, &at, &record.Text); err != nil {
			return nil, err
		}
		record.Time = time.Unix(0, at).UTC()
		out = append(out, record)
	}
	return out, rows.Err()
}

// Verdict returns the persisted verdict, if any.
func (s *V5Store) Verdict(ctx context.Context, runID string) (*core.Verdict, error) {
	var verdict core.Verdict
	var success int
	var evidence []byte
	err := s.db.QueryRowContext(ctx, `SELECT success,score,message,evidence FROM verdicts WHERE run_id=?`, runID).Scan(&success, &verdict.Score, &verdict.Message, &evidence)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	verdict.Success = success != 0
	verdict.Evidence = evidence
	return &verdict, nil
}

// Board reconstructs the current board from the read model and validates it.
func (s *V5Store) Board(ctx context.Context, runID string) (core.Board, error) {
	state, err := s.State(ctx, runID)
	if err != nil {
		return core.Board{}, err
	}
	return loadBoardQ(ctx, s.db, runID, state.BoardRevision)
}

// ListArtifacts returns the current artifact read model.
func (s *V5Store) ListArtifacts(ctx context.Context, runID string) ([]core.Artifact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM rm_artifacts WHERE run_id=? ORDER BY attempt_id,name`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Artifact
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var artifact core.Artifact
		if err := json.Unmarshal(payload, &artifact); err != nil {
			return nil, err
		}
		out = append(out, artifact)
	}
	return out, rows.Err()
}

// OpenArtifact reads and verifies one content-addressed blob.
func (s *V5Store) OpenArtifact(ctx context.Context, sha string) (io.ReadCloser, error) {
	if len(sha) != 64 {
		return nil, errors.New("artifact id must be 64 hex characters")
	}
	if _, err := hex.DecodeString(sha); err != nil {
		return nil, errors.New("artifact id must be hex")
	}
	var size int64
	var data []byte
	if err := s.db.QueryRowContext(ctx, `SELECT size,data FROM artifact_blobs WHERE sha256=?`, sha).Scan(&size, &data); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != sha {
		return nil, fmt.Errorf("%w: artifact %s content hash mismatch", core.ErrCorruptJournal, sha)
	}
	if int64(len(data)) != size {
		return nil, fmt.Errorf("%w: artifact %s size mismatch", core.ErrCorruptJournal, sha)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// VerifyChain verifies the entire per-run hash chain and its state head.
func (s *V5Store) VerifyChain(ctx context.Context, runID string) error {
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
	state, err := s.State(ctx, runID)
	if err != nil {
		return err
	}
	if uint64(len(records)) != state.HeadSeq {
		return fmt.Errorf("%w: journal has %d records, state head is %d", core.ErrCorruptJournal, len(records), state.HeadSeq)
	}
	if len(records) > 0 && records[len(records)-1].Hash != state.HeadHash {
		return fmt.Errorf("%w: journal head does not match state head", core.ErrCorruptJournal)
	}
	return nil
}

// NonTerminal lists non-terminal run states.
func (s *V5Store) NonTerminal(ctx context.Context) ([]core.State, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+stateColumns+` FROM run_state WHERE phase <> 'terminal' ORDER BY updated_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.State
	for rows.Next() {
		state, err := scanStateRow(rows, "")
		if err != nil {
			return nil, err
		}
		out = append(out, state)
	}
	return out, rows.Err()
}

func (s *V5Store) manifestDigest(ctx context.Context, runID string) (core.Manifest, string, error) {
	manifest, err := s.Manifest(ctx, runID)
	if err != nil {
		return core.Manifest{}, "", err
	}
	digest, err := manifest.Digest()
	if err != nil {
		return core.Manifest{}, "", err
	}
	return manifest, digest, nil
}

func nanosPtr(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	t := time.Unix(0, value.Int64).UTC()
	return &t
}

func scanStateRow(scanner interface{ Scan(...any) error }, wantRunID string) (core.State, error) {
	var state core.State
	var phase, outcome string
	var usage []byte
	var goal, stagnated int
	var started, finished sql.NullInt64
	err := scanner.Scan(&state.RunID, &phase, &outcome, &state.HeadSeq, &state.HeadHash, &state.BoardRevision, &usage, &goal, &state.NonGrowthRounds, &state.ReasonFailures, &stagnated, &started, &finished, &state.Error)
	if err != nil {
		return core.State{}, err
	}
	if wantRunID != "" && state.RunID != wantRunID {
		return core.State{}, fmt.Errorf("run state belongs to %q, not %q", state.RunID, wantRunID)
	}
	state.Phase, state.Outcome = core.Phase(phase), core.Outcome(outcome)
	state.GoalCandidate, state.Stagnated = goal != 0, stagnated != 0
	if len(usage) > 0 {
		if err := json.Unmarshal(usage, &state.Usage); err != nil {
			return core.State{}, fmt.Errorf("decode run usage: %w", err)
		}
	}
	state.StartedAt = nanosPtr(started)
	state.FinishedAt = nanosPtr(finished)
	if err := state.Validate(); err != nil {
		return core.State{}, fmt.Errorf("invalid persisted run state: %w", err)
	}
	return state, nil
}

func projectRecordsTx(ctx context.Context, tx *sql.Tx, runID string, records []core.Record) error {
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
			if err := upsertClaimTx(ctx, tx, record.Seq, p.Claim); err != nil {
				return err
			}
		case core.IntentProposedRecord:
			if err := upsertIntentTx(ctx, tx, record.Seq, p.Intent); err != nil {
				return err
			}
		case core.IntentClaimedRecord:
			intent, err := loadIntentTx(ctx, tx, runID, p.IntentID)
			if err != nil {
				return err
			}
			at := p.At.UTC()
			intent.State = core.IntentClaimed
			intent.ExecutorID = p.ExecutorID
			intent.ClaimedAt = &at
			if err := upsertIntentTx(ctx, tx, record.Seq, intent); err != nil {
				return err
			}
		case core.IntentConcludedRecord:
			intent, err := loadIntentTx(ctx, tx, runID, p.IntentID)
			if err != nil {
				return err
			}
			at := p.At.UTC()
			intent.State = core.IntentConcluded
			intent.ResultClaimID = p.ResultClaimID
			intent.ConcludedAt = &at
			if err := upsertIntentTx(ctx, tx, record.Seq, intent); err != nil {
				return err
			}
		case core.IntentAbandonedRecord:
			intent, err := loadIntentTx(ctx, tx, runID, p.IntentID)
			if err != nil {
				return err
			}
			intent.State = core.IntentAbandoned
			intent.Error = p.Reason
			if err := upsertIntentTx(ctx, tx, record.Seq, intent); err != nil {
				return err
			}
		case core.IntentReopenedRecord:
			intent, err := loadIntentTx(ctx, tx, runID, p.IntentID)
			if err != nil {
				return err
			}
			intent.State = core.IntentOpen
			intent.ExecutorID = ""
			intent.ClaimedAt = nil
			if err := upsertIntentTx(ctx, tx, record.Seq, intent); err != nil {
				return err
			}
		case core.AttemptStartedRecord:
			if err := upsertAttemptTx(ctx, tx, record.Seq, p.Attempt); err != nil {
				return err
			}
		case core.AttemptConcludedRecord:
			if err := updateAttemptProjectionTx(ctx, tx, runID, record.Seq, p.AttemptID, core.IntentAttemptConcluded, p.Conclusion, "", p.At); err != nil {
				return err
			}
		case core.AttemptFailedRecord:
			if err := updateAttemptProjectionTx(ctx, tx, runID, record.Seq, p.AttemptID, core.IntentAttemptFailed, "", p.Error, p.At); err != nil {
				return err
			}
		case core.AttemptCancelledRecord:
			if err := updateAttemptProjectionTx(ctx, tx, runID, record.Seq, p.AttemptID, core.IntentAttemptCancelled, "", "", p.At); err != nil {
				return err
			}
		case core.AttemptTimedOutRecord:
			if err := updateAttemptProjectionTx(ctx, tx, runID, record.Seq, p.AttemptID, core.IntentAttemptTimedOut, "", "", p.At); err != nil {
				return err
			}
		case core.ObservationCreatedRecord:
			if err := upsertObservationTx(ctx, tx, record.Seq, p.Observation); err != nil {
				return err
			}
		case core.ArtifactStoredRecord:
			if err := upsertArtifactTx(ctx, tx, record.Seq, p.Artifact); err != nil {
				return err
			}
		case core.RunResumedRecord:
			if err := normalizeResumeProjectionTx(ctx, tx, record.Seq, runID, p.Normalizations, record.At); err != nil {
				return err
			}
		}
	}
	return nil
}

// normalizeResumeProjectionTx mirrors core.Board.normalizeResume in the read
// model so Replay and the rm_* projection agree after a resume.
func normalizeResumeProjectionTx(ctx context.Context, tx *sql.Tx, seq uint64, runID string, normalizations []string, at time.Time) error {
	for _, normalization := range normalizations {
		switch normalization {
		case core.NormalizeAttemptsInterrupted:
			rows, err := tx.QueryContext(ctx, `SELECT payload FROM rm_attempts WHERE run_id=?`, runID)
			if err != nil {
				return err
			}
			var attempts []core.IntentAttempt
			for rows.Next() {
				var payload []byte
				if err := rows.Scan(&payload); err != nil {
					rows.Close()
					return err
				}
				var attempt core.IntentAttempt
				if err := json.Unmarshal(payload, &attempt); err != nil {
					rows.Close()
					return err
				}
				attempts = append(attempts, attempt)
			}
			if err := rows.Close(); err != nil {
				return err
			}
			if err := rows.Err(); err != nil {
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
				if err := upsertAttemptTx(ctx, tx, seq, attempt); err != nil {
					return err
				}
			}
		case core.NormalizeIntentsReopened:
			rows, err := tx.QueryContext(ctx, `SELECT payload FROM rm_intents WHERE run_id=?`, runID)
			if err != nil {
				return err
			}
			var intents []core.Intent
			for rows.Next() {
				var payload []byte
				if err := rows.Scan(&payload); err != nil {
					rows.Close()
					return err
				}
				var intent core.Intent
				if err := json.Unmarshal(payload, &intent); err != nil {
					rows.Close()
					return err
				}
				intents = append(intents, intent)
			}
			if err := rows.Close(); err != nil {
				return err
			}
			if err := rows.Err(); err != nil {
				return err
			}
			for _, intent := range intents {
				if intent.State != core.IntentClaimed {
					continue
				}
				intent.State = core.IntentOpen
				intent.ExecutorID = ""
				intent.ClaimedAt = nil
				if err := upsertIntentTx(ctx, tx, seq, intent); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unknown resume normalization %q", normalization)
		}
	}
	return nil
}

func upsertClaimTx(ctx context.Context, tx *sql.Tx, seq uint64, claim core.Claim) error {
	payload, err := core.CanonicalJSON(claim)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO rm_claims(run_id,id,seq,kind,payload) VALUES(?,?,?,?,?) ON CONFLICT(run_id,id) DO UPDATE SET seq=excluded.seq,kind=excluded.kind,payload=excluded.payload`,
		claim.RunID, claim.ID, seq, string(claim.Kind), payload)
	return err
}

func upsertIntentTx(ctx context.Context, tx *sql.Tx, seq uint64, intent core.Intent) error {
	payload, err := core.CanonicalJSON(intent)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO rm_intents(run_id,id,seq,state,payload) VALUES(?,?,?,?,?) ON CONFLICT(run_id,id) DO UPDATE SET seq=excluded.seq,state=excluded.state,payload=excluded.payload`,
		intent.RunID, intent.ID, seq, string(intent.State), payload)
	return err
}

func upsertAttemptTx(ctx context.Context, tx *sql.Tx, seq uint64, attempt core.IntentAttempt) error {
	payload, err := core.CanonicalJSON(attempt)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO rm_attempts(run_id,id,seq,intent_id,phase,payload) VALUES(?,?,?,?,?,?) ON CONFLICT(run_id,id) DO UPDATE SET seq=excluded.seq,intent_id=excluded.intent_id,phase=excluded.phase,payload=excluded.payload`,
		attempt.RunID, attempt.ID, seq, attempt.IntentID, string(attempt.Phase), payload)
	return err
}

func upsertObservationTx(ctx context.Context, tx *sql.Tx, seq uint64, observation core.Observation) error {
	payload, err := core.CanonicalJSON(observation)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO rm_observations(run_id,id,seq,intent_id,attempt_id,target,payload) VALUES(?,?,?,?,?,?,?) ON CONFLICT(run_id,id) DO UPDATE SET seq=excluded.seq,intent_id=excluded.intent_id,attempt_id=excluded.attempt_id,target=excluded.target,payload=excluded.payload`,
		observation.RunID, observation.ID, seq, observation.IntentID, observation.AttemptID, observation.Target, payload)
	return err
}

func upsertArtifactTx(ctx context.Context, tx *sql.Tx, seq uint64, artifact core.Artifact) error {
	payload, err := core.CanonicalJSON(artifact)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO rm_artifacts(run_id,artifact_id,attempt_id,name,sha256,size,media_type,redacted,payload) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(run_id,artifact_id,attempt_id,name) DO UPDATE SET sha256=excluded.sha256,size=excluded.size,media_type=excluded.media_type,redacted=excluded.redacted,payload=excluded.payload`,
		artifact.RunID, artifact.ID, artifact.AttemptID, artifact.Name, artifact.SHA256, artifact.Size, artifact.MediaType, boolInt(artifact.Redacted), payload)
	return err
}

func updateAttemptProjectionTx(ctx context.Context, tx *sql.Tx, runID string, seq uint64, attemptID string, phase core.IntentAttemptPhase, conclusion, failure string, at time.Time) error {
	var payload []byte
	err := tx.QueryRowContext(ctx, `SELECT payload FROM rm_attempts WHERE run_id=? AND id=?`, runID, attemptID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: attempt %s", core.ErrNotFound, attemptID)
	}
	if err != nil {
		return err
	}
	var attempt core.IntentAttempt
	if err := json.Unmarshal(payload, &attempt); err != nil {
		return err
	}
	attempt.Phase = phase
	attempt.Conclusion = conclusion
	attempt.Error = failure
	attempt.FinishedAt = at.UTC()
	return upsertAttemptTx(ctx, tx, seq, attempt)
}

func loadIntentTx(ctx context.Context, tx *sql.Tx, runID, intentID string) (core.Intent, error) {
	var payload []byte
	err := tx.QueryRowContext(ctx, `SELECT payload FROM rm_intents WHERE run_id=? AND id=?`, runID, intentID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return core.Intent{}, fmt.Errorf("%w: intent %s", core.ErrNotFound, intentID)
	}
	if err != nil {
		return core.Intent{}, err
	}
	var intent core.Intent
	if err := json.Unmarshal(payload, &intent); err != nil {
		return core.Intent{}, err
	}
	return intent, nil
}

func loadBoardTx(ctx context.Context, tx *sql.Tx, runID string, revision uint64) (core.Board, error) {
	return loadBoardQ(ctx, tx, runID, revision)
}

func loadBoardQ(ctx context.Context, q v5Queryer, runID string, revision uint64) (core.Board, error) {
	board := core.Board{RunID: runID, Revision: revision}
	rows, err := q.QueryContext(ctx, `SELECT payload FROM rm_claims WHERE run_id=? ORDER BY seq`, runID)
	if err != nil {
		return core.Board{}, err
	}
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			rows.Close()
			return core.Board{}, err
		}
		var claim core.Claim
		if err := json.Unmarshal(payload, &claim); err != nil {
			rows.Close()
			return core.Board{}, err
		}
		board.Claims = append(board.Claims, claim)
	}
	if err := rows.Close(); err != nil {
		return core.Board{}, err
	}
	if err := rows.Err(); err != nil {
		return core.Board{}, err
	}
	if err := loadPayloads(ctx, q, `SELECT payload FROM rm_intents WHERE run_id=? ORDER BY seq`, runID, &board.Intents); err != nil {
		return core.Board{}, err
	}
	if err := loadPayloads(ctx, q, `SELECT payload FROM rm_attempts WHERE run_id=? ORDER BY seq`, runID, &board.Attempts); err != nil {
		return core.Board{}, err
	}
	if err := loadPayloads(ctx, q, `SELECT payload FROM rm_observations WHERE run_id=? ORDER BY seq`, runID, &board.Observations); err != nil {
		return core.Board{}, err
	}
	if err := loadPayloads(ctx, q, `SELECT payload FROM rm_artifacts WHERE run_id=? ORDER BY attempt_id,name`, runID, &board.Artifacts); err != nil {
		return core.Board{}, err
	}
	if err := board.Validate(); err != nil {
		return core.Board{}, fmt.Errorf("%w: %v", core.ErrCorruptJournal, err)
	}
	return board, nil
}

func loadPayloads[T any](ctx context.Context, q v5Queryer, query, runID string, out *[]T) error {
	rows, err := q.QueryContext(ctx, query, runID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return err
		}
		var value T
		if err := json.Unmarshal(payload, &value); err != nil {
			return err
		}
		*out = append(*out, value)
	}
	return rows.Err()
}
