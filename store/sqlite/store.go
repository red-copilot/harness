package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/red-copilot/harness"
)

const schemaVersion = 1

// Store is the SQLite implementation of harness.Store.
type Store struct {
	db *sql.DB
}

// Open opens or creates the canonical harness database.
func Open(path string) (*Store, error) {
	if path == "" {
		path = ".harness/harness.db"
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open database file: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close database file: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("secure database file: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.configure(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) configure() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
		"PRAGMA synchronous=NORMAL",
	} {
		if _, err := s.db.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("%s: %w", pragma, err)
		}
	}
	return s.db.PingContext(ctx)
}

func (s *Store) migrate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS schema_meta (
			version INTEGER NOT NULL
		)`,
		`INSERT INTO schema_meta(version)
		 SELECT 0 WHERE NOT EXISTS (SELECT 1 FROM schema_meta)`,
		`CREATE TABLE IF NOT EXISTS runs (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			spec BLOB,
			result BLOB,
			error TEXT NOT NULL DEFAULT '',
			sweep_id TEXT NOT NULL DEFAULT '',
			metadata BLOB,
			created_at TEXT NOT NULL,
			started_at TEXT,
			finished_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS events (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
			session_id TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL,
			type TEXT NOT NULL,
			time TEXT NOT NULL,
			data BLOB,
			raw BLOB
		)`,
		`CREATE INDEX IF NOT EXISTS events_run_seq ON events(run_id, seq)`,
		`CREATE TABLE IF NOT EXISTS logs (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
			session_id TEXT NOT NULL DEFAULT '',
			stream TEXT NOT NULL,
			time TEXT NOT NULL,
			text TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS logs_run_seq ON logs(run_id, seq)`,
		`CREATE TABLE IF NOT EXISTS verdicts (
			run_id TEXT PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
			success INTEGER NOT NULL,
			score REAL NOT NULL DEFAULT 0,
			message TEXT NOT NULL DEFAULT '',
			evidence BLOB,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sweeps (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			spec BLOB,
			result BLOB,
			created_at TEXT NOT NULL,
			finished_at TEXT
		)`,
		`UPDATE schema_meta SET version = 1`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

func (s *Store) CreateRun(ctx context.Context, record harness.RunRecord) error {
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO runs(id, status, spec, result, error, sweep_id, metadata, created_at, started_at, finished_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, string(record.Status), []byte(record.Spec), []byte(record.Result), record.Error,
		record.SweepID, []byte(record.Metadata), formatTime(record.CreatedAt),
		formatOptionalTime(record.StartedAt), formatOptionalTime(record.FinishedAt))
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}
	return nil
}

func (s *Store) UpdateRun(ctx context.Context, record harness.RunRecord) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE runs SET status=?, spec=?, result=?, error=?, sweep_id=?, metadata=?, started_at=?, finished_at=?
		WHERE id=?`,
		string(record.Status), []byte(record.Spec), []byte(record.Result), record.Error,
		record.SweepID, []byte(record.Metadata), formatOptionalTime(record.StartedAt),
		formatOptionalTime(record.FinishedAt), record.ID)
	if err != nil {
		return fmt.Errorf("update run: %w", err)
	}
	return nil
}

func (s *Store) GetRun(ctx context.Context, id string) (harness.RunRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, status, spec, result, error, sweep_id, metadata, created_at, started_at, finished_at
		FROM runs WHERE id=?`, id)
	return scanRun(row)
}

func (s *Store) ListRuns(ctx context.Context, limit int) ([]harness.RunRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, status, spec, result, error, sweep_id, metadata, created_at, started_at, finished_at
		FROM runs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()
	out := make([]harness.RunRecord, 0)
	for rows.Next() {
		record, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(...any) error
}

func scanRun(row rowScanner) (harness.RunRecord, error) {
	var record harness.RunRecord
	var status string
	var spec, result, metadata []byte
	var createdAt string
	var startedAt, finishedAt sql.NullString
	if err := row.Scan(&record.ID, &status, &spec, &result, &record.Error, &record.SweepID,
		&metadata, &createdAt, &startedAt, &finishedAt); err != nil {
		return harness.RunRecord{}, fmt.Errorf("scan run: %w", err)
	}
	record.Status = harness.Status(status)
	record.Spec = append([]byte(nil), spec...)
	record.Result = append([]byte(nil), result...)
	record.Metadata = append([]byte(nil), metadata...)
	var err error
	record.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return harness.RunRecord{}, err
	}
	if startedAt.Valid {
		t, err := parseTime(startedAt.String)
		if err != nil {
			return harness.RunRecord{}, err
		}
		record.StartedAt = &t
	}
	if finishedAt.Valid {
		t, err := parseTime(finishedAt.String)
		if err != nil {
			return harness.RunRecord{}, err
		}
		record.FinishedAt = &t
	}
	return record, nil
}

func (s *Store) AppendEvent(ctx context.Context, event harness.Event) (harness.Event, error) {
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO events(run_id, session_id, source, type, time, data, raw)
		VALUES(?, ?, ?, ?, ?, ?, ?)`,
		event.RunID, event.SessionID, event.Source, event.Type, formatTime(event.Time),
		[]byte(event.Data), []byte(event.Raw))
	if err != nil {
		return harness.Event{}, fmt.Errorf("append event: %w", err)
	}
	event.Seq, err = res.LastInsertId()
	if err != nil {
		return harness.Event{}, fmt.Errorf("event sequence: %w", err)
	}
	return event, nil
}

func (s *Store) ListEvents(ctx context.Context, runID string, after int64) ([]harness.Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT seq, run_id, session_id, source, type, time, data, raw
		FROM events WHERE run_id=? AND seq>? ORDER BY seq`, runID, after)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	out := make([]harness.Event, 0)
	for rows.Next() {
		var event harness.Event
		var at string
		var data, raw []byte
		if err := rows.Scan(&event.Seq, &event.RunID, &event.SessionID, &event.Source,
			&event.Type, &at, &data, &raw); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		event.Time, err = parseTime(at)
		if err != nil {
			return nil, err
		}
		event.Data = append([]byte(nil), data...)
		event.Raw = append([]byte(nil), raw...)
		out = append(out, event)
	}
	return out, rows.Err()
}

func (s *Store) AppendLog(ctx context.Context, record harness.LogRecord) (harness.LogRecord, error) {
	if record.Time.IsZero() {
		record.Time = time.Now().UTC()
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO logs(run_id, session_id, stream, time, text)
		VALUES(?, ?, ?, ?, ?)`,
		record.RunID, record.SessionID, record.Stream, formatTime(record.Time), record.Text)
	if err != nil {
		return harness.LogRecord{}, fmt.Errorf("append log: %w", err)
	}
	record.Seq, err = res.LastInsertId()
	if err != nil {
		return harness.LogRecord{}, fmt.Errorf("log sequence: %w", err)
	}
	return record, nil
}

func (s *Store) ListLogs(ctx context.Context, runID string, after int64) ([]harness.LogRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT seq, run_id, session_id, stream, time, text
		FROM logs WHERE run_id=? AND seq>? ORDER BY seq`, runID, after)
	if err != nil {
		return nil, fmt.Errorf("list logs: %w", err)
	}
	defer rows.Close()
	return scanLogs(rows)
}

// ListLogsPage returns a bounded page of logs. When tail is true and after is
// zero, the newest limit rows are returned in ascending sequence order.
func (s *Store) ListLogsPage(ctx context.Context, runID string, after int64, limit int, tail bool) ([]harness.LogRecord, error) {
	if limit <= 0 {
		limit = 1000
	}
	var rows *sql.Rows
	var err error
	if tail && after == 0 {
		rows, err = s.db.QueryContext(ctx, `
			SELECT seq, run_id, session_id, stream, time, text
			FROM logs WHERE run_id=? ORDER BY seq DESC LIMIT ?`, runID, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT seq, run_id, session_id, stream, time, text
			FROM logs WHERE run_id=? AND seq>? ORDER BY seq LIMIT ?`, runID, after, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list logs page: %w", err)
	}
	defer rows.Close()
	out, err := scanLogs(rows)
	if err != nil {
		return nil, err
	}
	if tail && after == 0 {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out, nil
}

func scanLogs(rows *sql.Rows) ([]harness.LogRecord, error) {
	out := make([]harness.LogRecord, 0)
	for rows.Next() {
		var record harness.LogRecord
		var at string
		if err := rows.Scan(&record.Seq, &record.RunID, &record.SessionID, &record.Stream,
			&at, &record.Text); err != nil {
			return nil, fmt.Errorf("scan log: %w", err)
		}
		t, err := parseTime(at)
		if err != nil {
			return nil, err
		}
		record.Time = t
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) SaveVerdict(ctx context.Context, record harness.VerdictRecord) error {
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO verdicts(run_id, success, score, message, evidence, created_at)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id) DO UPDATE SET
			success=excluded.success, score=excluded.score, message=excluded.message,
			evidence=excluded.evidence, created_at=excluded.created_at`,
		record.RunID, boolInt(record.Success), record.Score, record.Message,
		[]byte(record.Evidence), formatTime(record.CreatedAt))
	if err != nil {
		return fmt.Errorf("save verdict: %w", err)
	}
	return nil
}

func (s *Store) GetVerdict(ctx context.Context, runID string) (harness.VerdictRecord, error) {
	var record harness.VerdictRecord
	var success int
	var evidence []byte
	var createdAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT run_id, success, score, message, evidence, created_at
		FROM verdicts WHERE run_id=?`, runID).Scan(
		&record.RunID, &success, &record.Score, &record.Message, &evidence, &createdAt)
	if err != nil {
		return harness.VerdictRecord{}, fmt.Errorf("get verdict: %w", err)
	}
	record.Success = success != 0
	record.Evidence = append([]byte(nil), evidence...)
	record.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return harness.VerdictRecord{}, err
	}
	return record, nil
}

func (s *Store) SaveSweep(ctx context.Context, record harness.SweepRecord) error {
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sweeps(id, status, spec, result, created_at, finished_at)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			status=excluded.status, spec=excluded.spec, result=excluded.result,
			created_at=excluded.created_at, finished_at=excluded.finished_at`,
		record.ID, string(record.Status), []byte(record.Spec), []byte(record.Result),
		formatTime(record.CreatedAt), formatOptionalTime(record.FinishedAt))
	if err != nil {
		return fmt.Errorf("save sweep: %w", err)
	}
	return nil
}

func (s *Store) GetSweep(ctx context.Context, id string) (harness.SweepRecord, error) {
	var record harness.SweepRecord
	var status string
	var spec, result []byte
	var createdAt string
	var finishedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, status, spec, result, created_at, finished_at
		FROM sweeps WHERE id=?`, id).Scan(
		&record.ID, &status, &spec, &result, &createdAt, &finishedAt)
	if err != nil {
		return harness.SweepRecord{}, fmt.Errorf("get sweep: %w", err)
	}
	record.Status = harness.Status(status)
	record.Spec = append([]byte(nil), spec...)
	record.Result = append([]byte(nil), result...)
	record.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return harness.SweepRecord{}, err
	}
	if finishedAt.Valid {
		t, err := parseTime(finishedAt.String)
		if err != nil {
			return harness.SweepRecord{}, err
		}
		record.FinishedAt = &t
	}
	return record, nil
}

func (s *Store) ListSweeps(ctx context.Context, limit int) ([]harness.SweepRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, status, spec, result, created_at, finished_at
		FROM sweeps ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list sweeps: %w", err)
	}
	defer rows.Close()
	out := make([]harness.SweepRecord, 0)
	for rows.Next() {
		var record harness.SweepRecord
		var status string
		var spec, result []byte
		var createdAt string
		var finishedAt sql.NullString
		if err := rows.Scan(&record.ID, &status, &spec, &result, &createdAt, &finishedAt); err != nil {
			return nil, fmt.Errorf("scan sweep: %w", err)
		}
		record.Status = harness.Status(status)
		record.Spec = append([]byte(nil), spec...)
		record.Result = append([]byte(nil), result...)
		record.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return nil, err
		}
		if finishedAt.Valid {
			t, err := parseTime(finishedAt.String)
			if err != nil {
				return nil, err
			}
			record.FinishedAt = &t
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) MarkInterrupted(ctx context.Context) error {
	now := formatTime(time.Now().UTC())
	if _, err := s.db.ExecContext(ctx, `
		UPDATE runs SET status=?, finished_at=?
		WHERE status IN (?, ?, ?)`,
		string(harness.StatusInterrupted), now,
		string(harness.StatusQueued), string(harness.StatusStarting), string(harness.StatusRunning)); err != nil {
		return fmt.Errorf("mark interrupted runs: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE sweeps SET status=?, finished_at=?
		WHERE status IN (?, ?, ?)`,
		string(harness.StatusInterrupted), now,
		string(harness.StatusQueued), string(harness.StatusStarting), string(harness.StatusRunning)); err != nil {
		return fmt.Errorf("mark interrupted sweeps: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func formatOptionalTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

func parseTime(value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse time %q: %w", value, err)
	}
	return t, nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

var _ harness.Store = (*Store)(nil)
var _ harness.LogPageReader = (*Store)(nil)
