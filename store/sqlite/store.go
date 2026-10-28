package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/red-copilot/harness/core"
	"github.com/red-copilot/harness/engine"
)

const schemaVersion = 7

type Store struct {
	db *sql.DB
}

// Open opens an empty or current v7 database. Legacy databases are rejected so
// old Runs cannot be upgraded in place and mistaken for v2 history.
func Open(path string) (*Store, error) {
	return openPath(path, false)
}

// OpenFresh creates a new v3 database and refuses to overwrite any existing
// database or WAL/SHM sidecar. The caller must first create and verify a backup.
func OpenFresh(path string) (*Store, error) {
	return openPath(path, true)
}

func openPath(path string, fresh bool) (*Store, error) {
	if path == "" {
		path = ".harness/harness.db"
	}
	if fresh {
		for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
			if _, err := os.Lstat(candidate); err == nil {
				return nil, fmt.Errorf("refusing to create fresh database over existing %s", candidate)
			} else if !os.IsNotExist(err) {
				return nil, fmt.Errorf("inspect fresh database target %s: %w", candidate, err)
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil && filepath.Dir(path) != "." {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open database file: %w", err)
	}
	_ = file.Close()
	_ = os.Chmod(path, 0o600)
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
	if fresh {
		if err := store.initializeFresh(); err != nil {
			_ = db.Close()
			return nil, err
		}
	} else if err := store.ensureCurrentSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) initializeFresh() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin fresh schema: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range schemaStatements() {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("initialize fresh schema: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE schema_meta SET version=?`, schemaVersion); err != nil {
		return fmt.Errorf("initialize schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit fresh schema: %w", err)
	}
	return nil
}

func (s *Store) configure() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON", "PRAGMA synchronous=NORMAL"} {
		if _, err := s.db.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("%s: %w", pragma, err)
		}
	}
	return s.db.PingContext(ctx)
}

func schemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS schema_meta (version INTEGER NOT NULL)`,
		`INSERT INTO schema_meta(version) SELECT 0 WHERE NOT EXISTS (SELECT 1 FROM schema_meta)`,

		`CREATE TABLE IF NOT EXISTS runs (id TEXT PRIMARY KEY, phase TEXT NOT NULL DEFAULT '', outcome TEXT NOT NULL DEFAULT '', spec BLOB, result BLOB, error TEXT NOT NULL DEFAULT '', sweep_id TEXT NOT NULL DEFAULT '', metadata BLOB, created_at TEXT NOT NULL, started_at TEXT, finished_at TEXT)`,
		`CREATE TABLE IF NOT EXISTS events (seq INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL, session_id TEXT NOT NULL DEFAULT '', source TEXT NOT NULL, type TEXT NOT NULL, time TEXT NOT NULL, data BLOB, raw BLOB, schema_version INTEGER NOT NULL DEFAULT 1, prev_hash TEXT NOT NULL DEFAULT '', hash TEXT NOT NULL DEFAULT '')`,
		`CREATE INDEX IF NOT EXISTS events_run_seq ON events(run_id, seq)`,
		`CREATE TRIGGER IF NOT EXISTS prevent_event_after_terminal BEFORE INSERT ON events WHEN EXISTS (SELECT 1 FROM runs WHERE id=NEW.run_id AND phase='terminal') BEGIN SELECT RAISE(ABORT, 'run is finalized'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_event_update BEFORE UPDATE ON events BEGIN SELECT RAISE(ABORT, 'events are append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_event_delete BEFORE DELETE ON events BEGIN SELECT RAISE(ABORT, 'events are append-only'); END`,
		`CREATE TABLE IF NOT EXISTS logs (seq INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL, session_id TEXT NOT NULL DEFAULT '', stream TEXT NOT NULL, time TEXT NOT NULL, text TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS logs_run_seq ON logs(run_id, seq)`,
		`CREATE TRIGGER IF NOT EXISTS prevent_log_update BEFORE UPDATE ON logs BEGIN SELECT RAISE(ABORT, 'logs are append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_log_delete BEFORE DELETE ON logs BEGIN SELECT RAISE(ABORT, 'logs are append-only'); END`,
		`CREATE TABLE IF NOT EXISTS verdicts (run_id TEXT PRIMARY KEY, success INTEGER NOT NULL, score REAL NOT NULL DEFAULT 0, message TEXT NOT NULL DEFAULT '', evidence BLOB, created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS sweeps (id TEXT PRIMARY KEY, status TEXT NOT NULL, spec BLOB, result BLOB, error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, finished_at TEXT)`,
		`CREATE TABLE IF NOT EXISTS artifact_blobs (sha256 TEXT PRIMARY KEY, size INTEGER NOT NULL, data BLOB NOT NULL)`,
		`CREATE TRIGGER IF NOT EXISTS prevent_blob_update BEFORE UPDATE ON artifact_blobs BEGIN SELECT RAISE(ABORT, 'artifact blobs are immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_blob_delete BEFORE DELETE ON artifact_blobs BEGIN SELECT RAISE(ABORT, 'artifact blobs are immutable'); END`,
		`CREATE TABLE IF NOT EXISTS artifacts (run_id TEXT NOT NULL, id TEXT NOT NULL, attempt_id TEXT NOT NULL, session_id TEXT NOT NULL, name TEXT NOT NULL, media_type TEXT NOT NULL, size INTEGER NOT NULL, sha256 TEXT NOT NULL, redacted INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, PRIMARY KEY(run_id, id, attempt_id, name))`,
		`CREATE TABLE IF NOT EXISTS claims (run_id TEXT NOT NULL, id TEXT NOT NULL, task_id TEXT NOT NULL, kind TEXT NOT NULL, source_intent_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, payload BLOB NOT NULL, PRIMARY KEY(run_id, id))`,
		`CREATE INDEX IF NOT EXISTS claims_run_created ON claims(run_id, created_at, id)`,
		`CREATE TABLE IF NOT EXISTS intents (run_id TEXT NOT NULL, id TEXT NOT NULL, kind TEXT NOT NULL, state TEXT NOT NULL, slot INTEGER NOT NULL DEFAULT 0, executor_id TEXT NOT NULL DEFAULT '', result_claim_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, claimed_at TEXT, concluded_at TEXT, error TEXT NOT NULL DEFAULT '', payload BLOB NOT NULL, PRIMARY KEY(run_id, id))`,
		`CREATE INDEX IF NOT EXISTS intents_run_created ON intents(run_id, created_at, id)`,
		`CREATE TABLE IF NOT EXISTS intent_sources (run_id TEXT NOT NULL, intent_id TEXT NOT NULL, claim_id TEXT NOT NULL, ordinal INTEGER NOT NULL, PRIMARY KEY(run_id, intent_id, claim_id))`,
		`CREATE TABLE IF NOT EXISTS hints (run_id TEXT NOT NULL, id TEXT NOT NULL, creator TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, payload BLOB NOT NULL, PRIMARY KEY(run_id, id))`,
		`CREATE INDEX IF NOT EXISTS hints_run_created ON hints(run_id, created_at, id)`,
		`CREATE TABLE IF NOT EXISTS attempts (run_id TEXT NOT NULL, id TEXT NOT NULL, intent_id TEXT NOT NULL, slot INTEGER NOT NULL, session_id TEXT NOT NULL, phase TEXT NOT NULL, conclusion TEXT NOT NULL DEFAULT '', started_at TEXT NOT NULL, finished_at TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '', payload BLOB NOT NULL, PRIMARY KEY(run_id, id))`,
		`CREATE INDEX IF NOT EXISTS attempts_run_started ON attempts(run_id, started_at, id)`,
		`CREATE TABLE IF NOT EXISTS observations (run_id TEXT NOT NULL, id TEXT NOT NULL, intent_id TEXT NOT NULL, attempt_id TEXT NOT NULL, kind TEXT NOT NULL, target TEXT NOT NULL, summary TEXT NOT NULL, created_at TEXT NOT NULL, payload BLOB NOT NULL, PRIMARY KEY(run_id, id))`,
		`CREATE TABLE IF NOT EXISTS observation_artifacts (run_id TEXT NOT NULL, observation_id TEXT NOT NULL, artifact_id TEXT NOT NULL, PRIMARY KEY(run_id, observation_id, artifact_id))`,
		`CREATE TABLE IF NOT EXISTS intent_versions (run_id TEXT NOT NULL, intent_id TEXT NOT NULL, version INTEGER NOT NULL, payload BLOB NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(run_id, intent_id, version))`,
		`CREATE TABLE IF NOT EXISTS attempt_versions (run_id TEXT NOT NULL, attempt_id TEXT NOT NULL, version INTEGER NOT NULL, payload BLOB NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(run_id, attempt_id, version))`,
		`CREATE TABLE IF NOT EXISTS board_commits (run_id TEXT NOT NULL, revision INTEGER NOT NULL, payload BLOB NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(run_id, revision))`,
	}
}

func (s *Store) ensureCurrentSchema() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var tableCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&tableCount); err != nil {
		return fmt.Errorf("inspect database schema: %w", err)
	}
	if tableCount == 0 {
		return s.initializeFresh()
	}
	var version int
	if err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_meta LIMIT 1`).Scan(&version); err != nil {
		return fmt.Errorf("existing database is not a current v7 database; create a verified backup and use OpenFresh: %w", err)
	}
	if version != schemaVersion {
		return fmt.Errorf("database schema version %d cannot be upgraded in place; create a verified backup and use OpenFresh", version)
	}
	for _, table := range []string{
		"schema_meta", "runs", "events", "logs", "verdicts", "sweeps", "artifact_blobs",
		"artifacts", "claims", "intents", "intent_sources", "hints", "attempts",
		"observations", "observation_artifacts", "intent_versions", "attempt_versions", "board_commits",
	} {
		var present int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&present); err != nil {
			return fmt.Errorf("inspect schema table %s: %w", table, err)
		}
		if present == 0 {
			return fmt.Errorf("current schema is missing table %s; restore a verified backup and use OpenFresh", table)
		}
	}
	return nil
}

func (s *Store) CreateRun(ctx context.Context, record core.RunRecord) error {
	phase, outcome, _, err := core.NormalizeRunState(record.Phase, record.Outcome, "")
	if err != nil {
		return fmt.Errorf("create run state: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO runs(id,phase,outcome,spec,result,error,metadata,created_at,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, record.ID, phase, outcome, nullableJSON(record.Spec), nullableJSON(record.Result), record.Error, nullableJSON(record.Metadata), formatTime(record.CreatedAt), formatTimePtr(record.StartedAt), formatTimePtr(record.FinishedAt))
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}
	return nil
}

func (s *Store) UpdateRun(ctx context.Context, record core.RunRecord) error {
	phase, outcome, _, stateErr := core.NormalizeRunState(record.Phase, record.Outcome, "")
	if stateErr != nil {
		return fmt.Errorf("update run state: %w", stateErr)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE runs SET phase=?, outcome=?, spec=?, result=?, error=?, metadata=?, started_at=?, finished_at=? WHERE id=? AND phase != ?`, phase, outcome, nullableJSON(record.Spec), nullableJSON(record.Result), record.Error, nullableJSON(record.Metadata), formatTimePtr(record.StartedAt), formatTimePtr(record.FinishedAt), record.ID, core.RunPhaseTerminal)
	if err != nil {
		return fmt.Errorf("update run: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("run %q not found or already terminal", record.ID)
	}
	return nil
}

func (s *Store) GetRun(ctx context.Context, id string) (core.RunRecord, error) {
	return scanRun(s.db.QueryRowContext(ctx, `SELECT id,phase,outcome,spec,result,error,metadata,created_at,started_at,finished_at FROM runs WHERE id=?`, id))
}
func (s *Store) ListRuns(ctx context.Context, limit int) ([]core.RunRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,phase,outcome,spec,result,error,metadata,created_at,started_at,finished_at FROM runs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.RunRecord
	for rows.Next() {
		record, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func scanRun(scanner interface{ Scan(...any) error }) (core.RunRecord, error) {
	var record core.RunRecord
	var phase, outcome, created string
	var spec, result, metadata []byte
	var started, finished sql.NullString
	if err := scanner.Scan(&record.ID, &phase, &outcome, &spec, &result, &record.Error, &metadata, &created, &started, &finished); err != nil {
		return record, err
	}
	var stateErr error
	record.Phase, record.Outcome, _, stateErr = core.NormalizeRunState(core.RunPhase(phase), core.RunOutcome(outcome), "")
	if stateErr != nil {
		return record, stateErr
	}
	record.Spec = spec
	record.Result = result
	record.Metadata = metadata
	record.CreatedAt = parseTime(created)
	record.StartedAt = parseTimePtr(started)
	record.FinishedAt = parseTimePtr(finished)
	return record, nil
}

func (s *Store) AppendEvent(ctx context.Context, event core.Event) (core.Event, error) {
	if event.SchemaVersion == 0 {
		event.SchemaVersion = 1
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.Event{}, fmt.Errorf("append event: %w", err)
	}
	defer tx.Rollback()
	stored, err := appendHashedEventTx(ctx, tx, event)
	if err != nil {
		return core.Event{}, fmt.Errorf("append event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return core.Event{}, fmt.Errorf("append event: %w", err)
	}
	return stored, nil
}
func (s *Store) ListEvents(ctx context.Context, runID string, after int64) ([]core.Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq,schema_version,run_id,session_id,source,type,time,data,raw FROM events WHERE run_id=? AND seq>? ORDER BY seq`, runID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Event
	for rows.Next() {
		var event core.Event
		var tm string
		var data, raw []byte
		if err := rows.Scan(&event.Seq, &event.SchemaVersion, &event.RunID, &event.SessionID, &event.Source, &event.Type, &tm, &data, &raw); err != nil {
			return nil, err
		}
		event.Time = parseTime(tm)
		event.Data = data
		event.Raw = raw
		out = append(out, event)
	}
	return out, rows.Err()
}
func (s *Store) AppendLog(ctx context.Context, record core.LogRecord) (core.LogRecord, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO logs(run_id,session_id,stream,time,text) VALUES(?,?,?,?,?)`, record.RunID, record.SessionID, record.Stream, formatTime(record.Time), record.Text)
	if err != nil {
		return core.LogRecord{}, err
	}
	record.Seq, _ = result.LastInsertId()
	return record, nil
}
func (s *Store) ListLogs(ctx context.Context, runID string, after int64) ([]core.LogRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq,run_id,session_id,stream,time,text FROM logs WHERE run_id=? AND seq>? ORDER BY seq`, runID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.LogRecord
	for rows.Next() {
		var record core.LogRecord
		var tm string
		if err := rows.Scan(&record.Seq, &record.RunID, &record.SessionID, &record.Stream, &tm, &record.Text); err != nil {
			return nil, err
		}
		record.Time = parseTime(tm)
		out = append(out, record)
	}
	return out, rows.Err()
}

// Finalize commits the terminal Run record, its verdict and the run.finalized
// event in one transaction, so the audit can never retain a terminal Run
// without its verdict or its finalization event.
func (s *Store) Finalize(ctx context.Context, record engine.FinalizationRecord) (core.Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.Event{}, fmt.Errorf("finalize run: %w", err)
	}
	defer tx.Rollback()
	phase, outcome, _, stateErr := core.NormalizeRunState(record.Run.Phase, record.Run.Outcome, "")
	if stateErr != nil {
		return core.Event{}, fmt.Errorf("finalize run state: %w", stateErr)
	}
	if outcome == core.RunOutcomeSucceeded && (record.Verdict == nil || !record.Verdict.Success) {
		return core.Event{}, fmt.Errorf("successful run requires a successful authoritative verdict")
	}
	event := record.Event
	if event.SchemaVersion == 0 {
		event.SchemaVersion = 1
	}
	event, err = appendHashedEventTx(ctx, tx, event)
	if err != nil {
		return core.Event{}, fmt.Errorf("finalize event: %w", err)
	}
	if record.Verdict != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO verdicts(run_id,success,score,message,evidence,created_at) VALUES(?,?,?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET success=excluded.success,score=excluded.score,message=excluded.message,evidence=excluded.evidence,created_at=excluded.created_at`, record.Verdict.RunID, record.Verdict.Success, record.Verdict.Score, record.Verdict.Message, nullableJSON(record.Verdict.Evidence), formatTime(record.Verdict.CreatedAt)); err != nil {
			return core.Event{}, fmt.Errorf("finalize verdict: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE runs SET phase=?, outcome=?, spec=?, result=?, error=?, metadata=?, started_at=?, finished_at=? WHERE id=?`, phase, outcome, nullableJSON(record.Run.Spec), nullableJSON(record.Run.Result), record.Run.Error, nullableJSON(record.Run.Metadata), formatTimePtr(record.Run.StartedAt), formatTimePtr(record.Run.FinishedAt), record.Run.ID)
	if err != nil {
		return core.Event{}, fmt.Errorf("finalize run: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return core.Event{}, fmt.Errorf("finalize run: %w", err)
	}
	if rows == 0 {
		return core.Event{}, fmt.Errorf("run %q not found", record.Run.ID)
	}
	if err := tx.Commit(); err != nil {
		return core.Event{}, fmt.Errorf("commit finalization: %w", err)
	}
	return event, nil
}

func (s *Store) GetVerdict(ctx context.Context, runID string) (core.VerdictRecord, error) {
	var record core.VerdictRecord
	var evidence []byte
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT run_id,success,score,message,evidence,created_at FROM verdicts WHERE run_id=?`, runID).Scan(&record.RunID, &record.Success, &record.Score, &record.Message, &evidence, &created)
	record.Evidence = evidence
	record.CreatedAt = parseTime(created)
	return record, err
}

func (s *Store) SaveArtifact(ctx context.Context, artifact core.Artifact) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO artifacts(run_id,id,attempt_id,session_id,name,media_type,size,sha256,redacted,created_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(run_id,id,attempt_id,name) DO UPDATE SET redacted=excluded.redacted`, artifact.RunID, artifact.ID, artifact.AttemptID, artifact.SessionID, artifact.Name, artifact.MediaType, artifact.Size, artifact.SHA256, artifact.Redacted, formatTime(artifact.CreatedAt))
	return err
}
func (s *Store) ListArtifacts(ctx context.Context, runID string) ([]core.Artifact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT run_id,id,attempt_id,session_id,name,media_type,size,sha256,redacted,created_at FROM artifacts WHERE run_id=? ORDER BY created_at,id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Artifact
	for rows.Next() {
		var a core.Artifact
		var tm string
		if err := rows.Scan(&a.RunID, &a.ID, &a.AttemptID, &a.SessionID, &a.Name, &a.MediaType, &a.Size, &a.SHA256, &a.Redacted, &tm); err != nil {
			return nil, err
		}
		a.CreatedAt = parseTime(tm)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) MarkInterrupted(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM runs WHERE phase <> ?`, core.RunPhaseTerminal)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	now := time.Now().UTC()
	for _, id := range ids {
		event, err := core.NewEvent(id, core.EventRunFinalized, core.RunFinalizedPayload{Outcome: core.RunOutcomeInterrupted})
		if err != nil {
			return err
		}
		event.Time, event.Source = now, "recovery"
		if _, err := appendEventTx(ctx, tx, event); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE runs SET phase=?, outcome=?, finished_at=? WHERE id=?`, core.RunPhaseTerminal, core.RunOutcomeInterrupted, formatTime(now), id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Close() error { return s.db.Close() }

func queryPayload[T any](ctx context.Context, db *sql.DB, query string, args ...any) ([]T, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var item T
		if err := json.Unmarshal(payload, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func scopedID(runID, id string) string { return runID + "\x00" + id }
func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return []byte(value)
}
func formatTime(value time.Time) string {
	if value.IsZero() {
		value = time.Now().UTC()
	}
	return value.UTC().Format(time.RFC3339Nano)
}
func formatTimePtr(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return formatTime(*value)
}
func parseTime(value string) time.Time { tm, _ := time.Parse(time.RFC3339Nano, value); return tm }
func parseTimePtr(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	tm := parseTime(value.String)
	return &tm
}

var _ engine.Store = (*Store)(nil)
