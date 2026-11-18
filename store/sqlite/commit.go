package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/red-copilot/harness/core"
	"github.com/red-copilot/harness/engine"
)

// Commit atomically persists one board revision, its normalized records and
// all corresponding canonical events.
func (s *Store) Commit(ctx context.Context, commit engine.Commit) ([]core.Event, error) {
	if err := validateCommitScope(commit); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var phase string
	if err := tx.QueryRowContext(ctx, `SELECT phase FROM runs WHERE id=?`, commit.RunID).Scan(&phase); err != nil {
		return nil, fmt.Errorf("load run for commit: %w", err)
	}
	if phase == string(core.RunPhaseTerminal) {
		return nil, fmt.Errorf("run %s is finalized", commit.RunID)
	}
	currentRevision, err := boardRevision(ctx, tx, commit.RunID)
	if err != nil {
		return nil, fmt.Errorf("load board revision: %w", err)
	}
	if commit.ExpectedRevision != currentRevision {
		return nil, fmt.Errorf("stale board revision: expected %d, current %d", commit.ExpectedRevision, currentRevision)
	}
	if commit.Revision <= currentRevision {
		return nil, fmt.Errorf("board revision %d must advance from %d", commit.Revision, currentRevision)
	}
	for _, claim := range commit.Claims {
		if err := saveClaimTx(ctx, tx, claim); err != nil {
			return nil, err
		}
	}
	for _, intent := range commit.Intents {
		if err := saveIntentTx(ctx, tx, intent); err != nil {
			return nil, err
		}
	}
	for _, attempt := range commit.Attempts {
		if err := saveAttemptTx(ctx, tx, attempt); err != nil {
			return nil, err
		}
	}
	for _, observation := range commit.Observations {
		if err := saveObservationTx(ctx, tx, observation); err != nil {
			return nil, err
		}
	}
	for _, artifact := range commit.Artifacts {
		if err := saveArtifactTx(ctx, tx, artifact); err != nil {
			return nil, err
		}
	}
	storedEvents := make([]core.Event, 0, len(commit.Events))
	for _, event := range commit.Events {
		stored, err := appendEventTx(ctx, tx, event)
		if err != nil {
			return nil, err
		}
		storedEvents = append(storedEvents, stored)
	}
	payload, err := json.Marshal(commit)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO board_commits(run_id,revision,payload,created_at) VALUES(?,?,?,?)`, commit.RunID, commit.Revision, payload, formatTime(time.Now().UTC())); err != nil {
		return nil, fmt.Errorf("save board commit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return storedEvents, nil
}

// validateRunScope reports the first record whose RunID differs from runID.
func validateRunScope[T any](kind, runID string, values []T, fields func(T) (id, recordRunID string)) error {
	for _, value := range values {
		id, recordRunID := fields(value)
		if recordRunID != runID {
			return fmt.Errorf("%s %q belongs to run %q, not %q", kind, id, recordRunID, runID)
		}
	}
	return nil
}

// validateCommitScope prevents a caller from mixing another Run's records or
// events into a canonical board commit.
func validateCommitScope(commit engine.Commit) error {
	if commit.RunID == "" {
		return fmt.Errorf("commit run_id is required")
	}
	runID := commit.RunID
	for _, err := range []error{
		validateRunScope("claim", runID, commit.Claims, func(v core.Claim) (string, string) { return v.ID, v.RunID }),
		validateRunScope("intent", runID, commit.Intents, func(v core.Intent) (string, string) { return v.ID, v.RunID }),
		validateRunScope("attempt", runID, commit.Attempts, func(v core.IntentAttempt) (string, string) { return v.ID, v.RunID }),
		validateRunScope("observation", runID, commit.Observations, func(v core.Observation) (string, string) { return v.ID, v.RunID }),
		validateRunScope("artifact", runID, commit.Artifacts, func(v core.Artifact) (string, string) { return v.ID, v.RunID }),
		validateRunScope("event", runID, commit.Events, func(v core.Event) (string, string) { return string(v.Type), v.RunID }),
	} {
		if err != nil {
			return err
		}
	}
	return nil
}

func saveClaimTx(ctx context.Context, tx *sql.Tx, claim core.Claim) error {
	payload, err := json.Marshal(claim)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO claims(run_id,id,task_id,kind,source_intent_id,created_at,payload) VALUES(?,?,?,?,?,?,?) ON CONFLICT(run_id,id) DO NOTHING`, claim.RunID, claim.ID, claim.TaskID, claim.Kind, claim.SourceIntentID, formatTime(claim.CreatedAt), payload)
	return err
}

func saveIntentTx(ctx context.Context, tx *sql.Tx, intent core.Intent) error {
	payload, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO intents(run_id,id,state,executor_id,result_claim_id,created_at,claimed_at,concluded_at,error,payload) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(run_id,id) DO UPDATE SET state=excluded.state,executor_id=excluded.executor_id,result_claim_id=excluded.result_claim_id,claimed_at=excluded.claimed_at,concluded_at=excluded.concluded_at,error=excluded.error,payload=excluded.payload`, intent.RunID, intent.ID, intent.State, intent.ExecutorID, intent.ResultClaimID, formatTime(intent.CreatedAt), formatTimePtr(intent.ClaimedAt), formatTimePtr(intent.ConcludedAt), intent.Error, payload); err != nil {
		return err
	}
	for ordinal, claimID := range intent.FromClaimIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO intent_sources(run_id,intent_id,claim_id,ordinal) VALUES(?,?,?,?) ON CONFLICT(run_id,intent_id,claim_id) DO NOTHING`, intent.RunID, intent.ID, claimID, ordinal); err != nil {
			return err
		}
	}
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM intent_versions WHERE run_id=? AND intent_id=?`, intent.RunID, intent.ID).Scan(&version); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO intent_versions(run_id,intent_id,version,payload,created_at) VALUES(?,?,?,?,?)`, intent.RunID, intent.ID, version, payload, formatTime(time.Now().UTC()))
	return err
}

func saveAttemptTx(ctx context.Context, tx *sql.Tx, attempt core.IntentAttempt) error {
	payload, err := json.Marshal(attempt)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO attempts(run_id,id,intent_id,session_id,phase,conclusion,started_at,finished_at,error,payload) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(run_id,id) DO UPDATE SET intent_id=excluded.intent_id,session_id=excluded.session_id,phase=excluded.phase,conclusion=excluded.conclusion,finished_at=excluded.finished_at,error=excluded.error,payload=excluded.payload`, attempt.RunID, attempt.ID, attempt.IntentID, attempt.SessionID, attempt.Phase, attempt.Conclusion, formatTime(attempt.StartedAt), formatTimeOrEmpty(attempt.FinishedAt), attempt.Error, payload); err != nil {
		return err
	}
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM attempt_versions WHERE run_id=? AND attempt_id=?`, attempt.RunID, attempt.ID).Scan(&version); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO attempt_versions(run_id,attempt_id,version,payload,created_at) VALUES(?,?,?,?,?)`, attempt.RunID, attempt.ID, version, payload, formatTime(time.Now().UTC()))
	return err
}

func saveObservationTx(ctx context.Context, tx *sql.Tx, observation core.Observation) error {
	payload, err := json.Marshal(observation)
	if err != nil {
		return err
	}
	intentID := observation.IntentID
	if _, err := tx.ExecContext(ctx, `INSERT INTO observations(run_id,id,intent_id,attempt_id,kind,target,summary,created_at,payload) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(run_id,id) DO UPDATE SET intent_id=excluded.intent_id,attempt_id=excluded.attempt_id,kind=excluded.kind,target=excluded.target,summary=excluded.summary,payload=excluded.payload`, observation.RunID, observation.ID, intentID, observation.AttemptID, observation.Kind, observation.Target, observation.Summary, formatTime(observation.CreatedAt), payload); err != nil {
		return err
	}
	for _, artifact := range observation.Evidence {
		if _, err := tx.ExecContext(ctx, `INSERT INTO observation_artifacts(run_id,observation_id,artifact_id) VALUES(?,?,?) ON CONFLICT(run_id,observation_id,artifact_id) DO NOTHING`, observation.RunID, observation.ID, artifact.ID); err != nil {
			return err
		}
	}
	return nil
}

func saveArtifactTx(ctx context.Context, tx *sql.Tx, artifact core.Artifact) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO artifacts(run_id,id,attempt_id,session_id,name,media_type,size,sha256,redacted,created_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(run_id,id,attempt_id,name) DO UPDATE SET redacted=excluded.redacted`, artifact.RunID, artifact.ID, artifact.AttemptID, artifact.SessionID, artifact.Name, artifact.MediaType, artifact.Size, artifact.SHA256, artifact.Redacted, formatTime(artifact.CreatedAt))
	return err
}

func appendEventTx(ctx context.Context, tx *sql.Tx, event core.Event) (core.Event, error) {
	return appendHashedEventTx(ctx, tx, event)
}
