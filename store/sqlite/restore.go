package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/red-copilot/harness/engine"
)

// LatestBoardCommit returns the newest committed board revision for one Run.
// The payload is the canonical Commit persisted by Store.Commit, so replaying
// its records reconstructs the exact board state the engine last published.
func (s *Store) LatestBoardCommit(ctx context.Context, runID string) (engine.Commit, error) {
	var payload []byte
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM board_commits WHERE run_id=? ORDER BY revision DESC LIMIT 1`, runID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return engine.Commit{}, fmt.Errorf("run %s has no committed board revision", runID)
	}
	if err != nil {
		return engine.Commit{}, fmt.Errorf("load board commit: %w", err)
	}
	var commit engine.Commit
	if err := json.Unmarshal(payload, &commit); err != nil {
		return engine.Commit{}, fmt.Errorf("decode board commit for run %s: %w", runID, err)
	}
	if commit.RunID != runID {
		return engine.Commit{}, fmt.Errorf("board commit for run %s carries run_id %q", runID, commit.RunID)
	}
	return commit, nil
}

// RestoreBoardState validates one replayed board commit against the run's
// committed history before the engine adopts it. A damaged snapshot or a
// revision that does not match the persisted board records fails closed.
func (s *Store) RestoreBoardState(ctx context.Context, runID string) (engine.Commit, error) {
	commit, err := s.LatestBoardCommit(ctx, runID)
	if err != nil {
		return engine.Commit{}, err
	}
	persisted, err := boardRevision(ctx, s.db, runID)
	if err != nil {
		return engine.Commit{}, fmt.Errorf("verify board revision: %w", err)
	}
	if commit.Revision != persisted {
		return engine.Commit{}, fmt.Errorf("board commit revision %d does not match persisted revision %d", commit.Revision, persisted)
	}
	claims, err := s.ListClaims(ctx, runID)
	if err != nil {
		return engine.Commit{}, fmt.Errorf("verify claims: %w", err)
	}
	if len(claims) < len(commit.Claims) {
		return engine.Commit{}, fmt.Errorf("board commit claims missing from store: %d < %d", len(claims), len(commit.Claims))
	}
	return commit, nil
}

var _ interface {
	LatestBoardCommit(context.Context, string) (engine.Commit, error)
	RestoreBoardState(context.Context, string) (engine.Commit, error)
} = (*Store)(nil)
