package sqlite

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/red-copilot/harness/core"
)

func (s *Store) SaveFact(ctx context.Context, fact core.Fact) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveFactTx(ctx, tx, fact); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListFacts(ctx context.Context, runID string) ([]core.Fact, error) {
	return queryPayload[core.Fact](ctx, s.db, `SELECT payload FROM facts WHERE run_id=? ORDER BY created_at,id`, runID)
}

func (s *Store) SaveIntent(ctx context.Context, intent core.Intent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveIntentTx(ctx, tx, intent); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListIntents(ctx context.Context, runID string) ([]core.Intent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM intents WHERE run_id=? ORDER BY created_at,id`, runID)
	if err != nil {
		return nil, err
	}
	var payloads [][]byte
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			rows.Close()
			return nil, err
		}
		payloads = append(payloads, append([]byte(nil), payload...))
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]core.Intent, 0, len(payloads))
	for _, payload := range payloads {
		var intent core.Intent
		if err := json.Unmarshal(payload, &intent); err != nil {
			return nil, err
		}
		sourceRows, err := s.db.QueryContext(ctx, `SELECT fact_id FROM intent_sources WHERE run_id=? AND intent_id=? ORDER BY ordinal`, runID, intent.ID)
		if err != nil {
			return nil, err
		}
		var sources []string
		for sourceRows.Next() {
			var factID string
			if err := sourceRows.Scan(&factID); err != nil {
				sourceRows.Close()
				return nil, err
			}
			sources = append(sources, factID)
		}
		if err := sourceRows.Err(); err != nil {
			sourceRows.Close()
			return nil, err
		}
		if err := sourceRows.Close(); err != nil {
			return nil, err
		}
		if len(sources) > 0 {
			intent.FromFactIDs = sources
		}
		out = append(out, intent)
	}
	return out, nil
}

func (s *Store) SaveHint(ctx context.Context, hint core.Hint) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveHintTx(ctx, tx, hint); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListHints(ctx context.Context, runID string) ([]core.Hint, error) {
	return queryPayload[core.Hint](ctx, s.db, `SELECT payload FROM hints WHERE run_id=? ORDER BY created_at,id`, runID)
}

func (s *Store) SaveIntentAttempt(ctx context.Context, attempt core.IntentAttempt) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveAttemptTx(ctx, tx, attempt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListIntentAttempts(ctx context.Context, runID string) ([]core.IntentAttempt, error) {
	return queryPayload[core.IntentAttempt](ctx, s.db, `SELECT payload FROM exploration_attempts_v2 WHERE run_id=? ORDER BY started_at,id`, runID)
}

func (s *Store) SaveBlackboardObservation(ctx context.Context, observation core.Observation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveObservationTx(ctx, tx, observation); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListBlackboardObservations(ctx context.Context, runID string) ([]core.Observation, error) {
	return queryPayload[core.Observation](ctx, s.db, `SELECT payload FROM exploration_observations_v2 WHERE run_id=? ORDER BY created_at,id`, runID)
}

var _ interface {
	SaveFact(context.Context, core.Fact) error
	ListFacts(context.Context, string) ([]core.Fact, error)
	SaveIntent(context.Context, core.Intent) error
	ListIntents(context.Context, string) ([]core.Intent, error)
	SaveHint(context.Context, core.Hint) error
	ListHints(context.Context, string) ([]core.Hint, error)
	SaveIntentAttempt(context.Context, core.IntentAttempt) error
	ListIntentAttempts(context.Context, string) ([]core.IntentAttempt, error)
	SaveBlackboardObservation(context.Context, core.Observation) error
	ListBlackboardObservations(context.Context, string) ([]core.Observation, error)
} = (*Store)(nil)

func describeBlackboardError(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("blackboard store %s: %w", op, err)
}

func (s *Store) GetBoardRevision(ctx context.Context, runID string) (uint64, error) {
	var revision uint64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision),0) FROM board_commits WHERE run_id=?`, runID).Scan(&revision)
	return revision, err
}
