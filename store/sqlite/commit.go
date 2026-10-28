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
	for _, fact := range commit.Facts {
		if err := saveFactTx(ctx, tx, fact); err != nil {
			return nil, err
		}
	}
	for _, intent := range commit.Intents {
		if err := saveIntentTx(ctx, tx, intent); err != nil {
			return nil, err
		}
	}
	for _, hint := range commit.Hints {
		if err := saveHintTx(ctx, tx, hint); err != nil {
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

func saveFactTx(ctx context.Context, tx *sql.Tx, fact core.Fact) error {
	payload, err := json.Marshal(fact)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO facts(run_id,id,task_id,kind,source_intent_id,created_at,payload) VALUES(?,?,?,?,?,?,?) ON CONFLICT(run_id,id) DO NOTHING`, fact.RunID, fact.ID, fact.TaskID, fact.Kind, fact.SourceIntentID, formatTime(fact.CreatedAt), payload)
	return err
}

func saveIntentTx(ctx context.Context, tx *sql.Tx, intent core.Intent) error {
	payload, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO intents(run_id,id,kind,state,slot,executor_id,result_fact_id,created_at,claimed_at,concluded_at,error,payload) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(run_id,id) DO UPDATE SET kind=excluded.kind,state=excluded.state,slot=excluded.slot,executor_id=excluded.executor_id,result_fact_id=excluded.result_fact_id,claimed_at=excluded.claimed_at,concluded_at=excluded.concluded_at,error=excluded.error,payload=excluded.payload`, intent.RunID, intent.ID, intent.Kind, intent.State, intent.Slot, intent.ExecutorID, intent.ResultFactID, formatTime(intent.CreatedAt), formatTimePtr(intent.ClaimedAt), formatTimePtr(intent.ConcludedAt), intent.Error, payload); err != nil {
		return err
	}
	for ordinal, factID := range intent.FromFactIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO intent_sources(run_id,intent_id,fact_id,ordinal) VALUES(?,?,?,?) ON CONFLICT(run_id,intent_id,fact_id) DO NOTHING`, intent.RunID, intent.ID, factID, ordinal); err != nil {
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

func saveHintTx(ctx context.Context, tx *sql.Tx, hint core.Hint) error {
	payload, err := json.Marshal(hint)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO hints(run_id,id,creator,created_at,payload) VALUES(?,?,?,?,?) ON CONFLICT(run_id,id) DO NOTHING`, hint.RunID, hint.ID, hint.Creator, formatTime(hint.CreatedAt), payload)
	return err
}

func saveAttemptTx(ctx context.Context, tx *sql.Tx, attempt core.IntentAttempt) error {
	payload, err := json.Marshal(attempt)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO exploration_attempts_v2(run_id,id,intent_id,slot,session_id,phase,conclusion,started_at,finished_at,error,payload) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(run_id,id) DO UPDATE SET intent_id=excluded.intent_id,slot=excluded.slot,session_id=excluded.session_id,phase=excluded.phase,conclusion=excluded.conclusion,finished_at=excluded.finished_at,error=excluded.error,payload=excluded.payload`, attempt.RunID, attempt.ID, attempt.IntentID, attempt.Slot, attempt.SessionID, attempt.Phase, attempt.Conclusion, formatTime(attempt.StartedAt), formatTime(attempt.FinishedAt), attempt.Error, payload); err != nil {
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO exploration_observations_v2(run_id,id,intent_id,attempt_id,kind,target,summary,created_at,payload) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(run_id,id) DO UPDATE SET intent_id=excluded.intent_id,attempt_id=excluded.attempt_id,kind=excluded.kind,target=excluded.target,summary=excluded.summary,payload=excluded.payload`, observation.RunID, observation.ID, intentID, observation.AttemptID, observation.Kind, observation.Target, observation.Summary, formatTime(observation.CreatedAt), payload); err != nil {
		return err
	}
	for _, artifact := range observation.Evidence {
		if _, err := tx.ExecContext(ctx, `INSERT INTO exploration_observation_artifacts_v2(run_id,observation_id,artifact_id) VALUES(?,?,?) ON CONFLICT(run_id,observation_id,artifact_id) DO NOTHING`, observation.RunID, observation.ID, artifact.ID); err != nil {
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
	inserted, err := tx.ExecContext(ctx, `INSERT INTO events(run_id,session_id,source,type,time,data,raw,schema_version) VALUES(?,?,?,?,?,?,?,?)`, event.RunID, event.SessionID, event.Source, event.Type, formatTime(event.Time), nullableJSON(event.Data), nullableJSON(event.Raw), event.SchemaVersion)
	if err != nil {
		return core.Event{}, err
	}
	event.Seq, err = inserted.LastInsertId()
	return event, err
}
