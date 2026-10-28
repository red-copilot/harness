package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/red-copilot/harness/core"
)

// SaveSweep persists only the batch summary. Each member Run remains governed
// by CanonicalWriter and has its own terminal audit record.
func (s *Store) SaveSweep(ctx context.Context, record core.SweepRecord) error {
	if record.ID == "" {
		return fmt.Errorf("sweep id is required")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sweeps(id,status,result,error,created_at,finished_at) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,result=excluded.result,error=excluded.error,finished_at=excluded.finished_at`, record.ID, record.Status, nullableJSON(record.Result), record.Error, formatTime(record.CreatedAt), formatTimePtr(record.FinishedAt))
	if err != nil {
		return fmt.Errorf("save sweep: %w", err)
	}
	return nil
}

func (s *Store) GetSweep(ctx context.Context, id string) (core.SweepRecord, error) {
	var record core.SweepRecord
	var status, created string
	var result []byte
	var finished sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id,status,result,error,created_at,finished_at FROM sweeps WHERE id=?`, id).Scan(&record.ID, &status, &result, &record.Error, &created, &finished)
	if err != nil {
		return record, fmt.Errorf("get sweep: %w", err)
	}
	record.Status = core.Status(status)
	record.Result = append(json.RawMessage(nil), result...)
	record.CreatedAt = parseTime(created)
	record.FinishedAt = parseTimePtr(finished)
	return record, nil
}

func (s *Store) ListSweeps(ctx context.Context, limit int) ([]core.SweepRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,status,result,error,created_at,finished_at FROM sweeps ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list sweeps: %w", err)
	}
	defer rows.Close()
	var out []core.SweepRecord
	for rows.Next() {
		var record core.SweepRecord
		var status, created string
		var result []byte
		var finished sql.NullString
		if err := rows.Scan(&record.ID, &status, &result, &record.Error, &created, &finished); err != nil {
			return nil, err
		}
		record.Status = core.Status(status)
		record.Result = append(json.RawMessage(nil), result...)
		record.CreatedAt = parseTime(created)
		record.FinishedAt = parseTimePtr(finished)
		out = append(out, record)
	}
	return out, rows.Err()
}
