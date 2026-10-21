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

	"github.com/red-copilot/harness"
)

const schemaVersion = 3

type Store struct {
	db           *sql.DB
	artifactRoot string
}

func Open(path string) (*Store, error) {
	if path == "" {
		path = ".harness/harness.db"
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
	store := &Store{db: db, artifactRoot: filepath.Join(filepath.Dir(path), "artifacts", "sha256")}
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
	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON", "PRAGMA synchronous=NORMAL"} {
		if _, err := s.db.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("%s: %w", pragma, err)
		}
	}
	return s.db.PingContext(ctx)
}

func (s *Store) migrate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS schema_meta (version INTEGER NOT NULL)`,
		`INSERT INTO schema_meta(version) SELECT 0 WHERE NOT EXISTS (SELECT 1 FROM schema_meta)`,
		`CREATE TABLE IF NOT EXISTS runs (id TEXT PRIMARY KEY, status TEXT NOT NULL, spec BLOB, result BLOB, error TEXT NOT NULL DEFAULT '', sweep_id TEXT NOT NULL DEFAULT '', metadata BLOB, created_at TEXT NOT NULL, started_at TEXT, finished_at TEXT)`,
		`CREATE TABLE IF NOT EXISTS events (seq INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL, session_id TEXT NOT NULL DEFAULT '', source TEXT NOT NULL, type TEXT NOT NULL, time TEXT NOT NULL, data BLOB, raw BLOB, schema_version INTEGER NOT NULL DEFAULT 1)`,
		`CREATE INDEX IF NOT EXISTS events_run_seq ON events(run_id, seq)`,
		`CREATE TABLE IF NOT EXISTS logs (seq INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL, session_id TEXT NOT NULL DEFAULT '', stream TEXT NOT NULL, time TEXT NOT NULL, text TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS logs_run_seq ON logs(run_id, seq)`,
		`CREATE TABLE IF NOT EXISTS verdicts (run_id TEXT PRIMARY KEY, success INTEGER NOT NULL, score REAL NOT NULL DEFAULT 0, message TEXT NOT NULL DEFAULT '', evidence BLOB, created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS sweeps (id TEXT PRIMARY KEY, status TEXT NOT NULL, spec BLOB, result BLOB, error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, finished_at TEXT)`,
		`CREATE TABLE IF NOT EXISTS exploration_branches (id TEXT PRIMARY KEY, parent_id TEXT NOT NULL DEFAULT '', task_id TEXT NOT NULL, run_id TEXT NOT NULL DEFAULT '', depth INTEGER NOT NULL, topic TEXT NOT NULL, goal TEXT NOT NULL DEFAULT '', hypothesis TEXT NOT NULL DEFAULT '', success_condition TEXT NOT NULL DEFAULT '', failure_condition TEXT NOT NULL DEFAULT '', scope TEXT NOT NULL, budget BLOB, status TEXT NOT NULL, priority INTEGER NOT NULL DEFAULT 0, knowledge_refs BLOB, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, payload BLOB)`,
		`CREATE TABLE IF NOT EXISTS exploration_attempts (id TEXT PRIMARY KEY, run_id TEXT NOT NULL, branch_id TEXT NOT NULL, session_id TEXT NOT NULL, status TEXT NOT NULL, outcome TEXT NOT NULL, observation_ids BLOB, started_at TEXT NOT NULL, finished_at TEXT NOT NULL, error TEXT NOT NULL DEFAULT '', payload BLOB)`,
		`CREATE TABLE IF NOT EXISTS exploration_observations (id TEXT PRIMARY KEY, run_id TEXT NOT NULL, branch_id TEXT NOT NULL, attempt_id TEXT NOT NULL, kind TEXT NOT NULL, target TEXT NOT NULL, summary TEXT NOT NULL, evidence BLOB, created_at TEXT NOT NULL, payload BLOB)`,
		`CREATE TABLE IF NOT EXISTS exploration_observation_artifacts (run_id TEXT NOT NULL, observation_id TEXT NOT NULL, artifact_id TEXT NOT NULL, PRIMARY KEY(run_id, observation_id, artifact_id))`,
		`CREATE TABLE IF NOT EXISTS artifacts (run_id TEXT NOT NULL, id TEXT NOT NULL, attempt_id TEXT NOT NULL, session_id TEXT NOT NULL, name TEXT NOT NULL, media_type TEXT NOT NULL, size INTEGER NOT NULL, sha256 TEXT NOT NULL, redacted INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, PRIMARY KEY(run_id, id, attempt_id, name))`,
		`CREATE TABLE IF NOT EXISTS knowledge (run_id TEXT NOT NULL, id TEXT NOT NULL, task_id TEXT NOT NULL, version INTEGER NOT NULL, payload BLOB NOT NULL, PRIMARY KEY(run_id, id, version))`,
		`CREATE TABLE IF NOT EXISTS knowledge_sources (run_id TEXT NOT NULL, knowledge_id TEXT NOT NULL, version INTEGER NOT NULL, source BLOB NOT NULL, PRIMARY KEY(run_id, knowledge_id, version, source))`,
		`CREATE TABLE IF NOT EXISTS knowledge_versions (run_id TEXT NOT NULL, knowledge_id TEXT NOT NULL, version INTEGER NOT NULL, payload BLOB NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(run_id, knowledge_id, version))`,
	}
	for _, stmt := range statements {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate v3: %w", err)
		}
	}
	// Additive upgrades for databases created by pre-v3 binaries.
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE events ADD COLUMN schema_version INTEGER NOT NULL DEFAULT 1`)
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE exploration_branches ADD COLUMN payload BLOB`)
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE exploration_attempts ADD COLUMN payload BLOB`)
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE exploration_observations ADD COLUMN payload BLOB`)
	_, err := s.db.ExecContext(ctx, `UPDATE schema_meta SET version=?`, schemaVersion)
	if err != nil {
		return fmt.Errorf("update schema version: %w", err)
	}
	return nil
}

func (s *Store) CreateRun(ctx context.Context, record harness.RunRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO runs(id,status,spec,result,error,sweep_id,metadata,created_at,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, record.ID, record.Status, nullableJSON(record.Spec), nullableJSON(record.Result), record.Error, record.SweepID, nullableJSON(record.Metadata), formatTime(record.CreatedAt), formatTimePtr(record.StartedAt), formatTimePtr(record.FinishedAt))
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}
	return nil
}

func (s *Store) UpdateRun(ctx context.Context, record harness.RunRecord) error {
	result, err := s.db.ExecContext(ctx, `UPDATE runs SET status=?, spec=?, result=?, error=?, sweep_id=?, metadata=?, started_at=?, finished_at=? WHERE id=?`, record.Status, nullableJSON(record.Spec), nullableJSON(record.Result), record.Error, record.SweepID, nullableJSON(record.Metadata), formatTimePtr(record.StartedAt), formatTimePtr(record.FinishedAt), record.ID)
	if err != nil {
		return fmt.Errorf("update run: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("run %q not found", record.ID)
	}
	return nil
}

func (s *Store) GetRun(ctx context.Context, id string) (harness.RunRecord, error) {
	return scanRun(s.db.QueryRowContext(ctx, `SELECT id,status,spec,result,error,sweep_id,metadata,created_at,started_at,finished_at FROM runs WHERE id=?`, id))
}
func (s *Store) ListRuns(ctx context.Context, limit int) ([]harness.RunRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,status,spec,result,error,sweep_id,metadata,created_at,started_at,finished_at FROM runs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []harness.RunRecord
	for rows.Next() {
		record, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func scanRun(scanner interface{ Scan(...any) error }) (harness.RunRecord, error) {
	var record harness.RunRecord
	var status, created string
	var spec, result, metadata []byte
	var started, finished sql.NullString
	if err := scanner.Scan(&record.ID, &status, &spec, &result, &record.Error, &record.SweepID, &metadata, &created, &started, &finished); err != nil {
		return record, err
	}
	record.Status = harness.Status(status)
	record.Spec = spec
	record.Result = result
	record.Metadata = metadata
	record.CreatedAt = parseTime(created)
	record.StartedAt = parseTimePtr(started)
	record.FinishedAt = parseTimePtr(finished)
	return record, nil
}

func (s *Store) AppendEvent(ctx context.Context, event harness.Event) (harness.Event, error) {
	if event.SchemaVersion == 0 {
		event.SchemaVersion = 1
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO events(run_id,session_id,source,type,time,data,raw,schema_version) VALUES(?,?,?,?,?,?,?,?)`, event.RunID, event.SessionID, event.Source, event.Type, formatTime(event.Time), nullableJSON(event.Data), nullableJSON(event.Raw), event.SchemaVersion)
	if err != nil {
		return harness.Event{}, fmt.Errorf("append event: %w", err)
	}
	event.Seq, _ = result.LastInsertId()
	return event, nil
}
func (s *Store) ListEvents(ctx context.Context, runID string, after int64) ([]harness.Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq,schema_version,run_id,session_id,source,type,time,data,raw FROM events WHERE run_id=? AND seq>? ORDER BY seq`, runID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []harness.Event
	for rows.Next() {
		var event harness.Event
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
func (s *Store) AppendLog(ctx context.Context, record harness.LogRecord) (harness.LogRecord, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO logs(run_id,session_id,stream,time,text) VALUES(?,?,?,?,?)`, record.RunID, record.SessionID, record.Stream, formatTime(record.Time), record.Text)
	if err != nil {
		return harness.LogRecord{}, err
	}
	record.Seq, _ = result.LastInsertId()
	return record, nil
}
func (s *Store) ListLogs(ctx context.Context, runID string, after int64) ([]harness.LogRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq,run_id,session_id,stream,time,text FROM logs WHERE run_id=? AND seq>? ORDER BY seq`, runID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []harness.LogRecord
	for rows.Next() {
		var record harness.LogRecord
		var tm string
		if err := rows.Scan(&record.Seq, &record.RunID, &record.SessionID, &record.Stream, &tm, &record.Text); err != nil {
			return nil, err
		}
		record.Time = parseTime(tm)
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) SaveVerdict(ctx context.Context, record harness.VerdictRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO verdicts(run_id,success,score,message,evidence,created_at) VALUES(?,?,?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET success=excluded.success,score=excluded.score,message=excluded.message,evidence=excluded.evidence,created_at=excluded.created_at`, record.RunID, record.Success, record.Score, record.Message, nullableJSON(record.Evidence), formatTime(record.CreatedAt))
	return err
}
func (s *Store) GetVerdict(ctx context.Context, runID string) (harness.VerdictRecord, error) {
	var record harness.VerdictRecord
	var evidence []byte
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT run_id,success,score,message,evidence,created_at FROM verdicts WHERE run_id=?`, runID).Scan(&record.RunID, &record.Success, &record.Score, &record.Message, &evidence, &created)
	record.Evidence = evidence
	record.CreatedAt = parseTime(created)
	return record, err
}

func (s *Store) SaveBranch(ctx context.Context, branch harness.Branch) error {
	payload, _ := json.Marshal(branch)
	refs, _ := json.Marshal([]string{})
	_, err := s.db.ExecContext(ctx, `INSERT INTO exploration_branches(id,parent_id,task_id,run_id,depth,topic,goal,hypothesis,success_condition,failure_condition,scope,budget,status,priority,knowledge_refs,created_at,updated_at,payload) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,priority=excluded.priority,payload=excluded.payload,updated_at=excluded.updated_at`, scopedID(branch.RunID, branch.ID), branch.ParentID, branch.TaskID, branch.RunID, branch.Depth, branch.Topic, branch.Goal, branch.Hypothesis, branch.SuccessCondition, branch.FailureCondition, branch.Scope, []byte(nil), branch.Status, branch.Priority, refs, formatTime(branch.CreatedAt), formatTime(branch.UpdatedAt), payload)
	return err
}
func (s *Store) ListBranches(ctx context.Context, runID string) ([]harness.Branch, error) {
	return queryPayload[harness.Branch](ctx, s.db, `SELECT payload FROM exploration_branches WHERE run_id=? ORDER BY created_at,id`, runID)
}
func (s *Store) SaveAttempt(ctx context.Context, attempt harness.Attempt) error {
	payload, _ := json.Marshal(attempt)
	ids, _ := json.Marshal(attempt.ObservationIDs)
	_, err := s.db.ExecContext(ctx, `INSERT INTO exploration_attempts(id,run_id,branch_id,session_id,status,outcome,observation_ids,started_at,finished_at,error,payload) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,outcome=excluded.outcome,observation_ids=excluded.observation_ids,finished_at=excluded.finished_at,error=excluded.error,payload=excluded.payload`, scopedID(attempt.RunID, attempt.ID), attempt.RunID, attempt.BranchID, attempt.SessionID, attempt.Status, attempt.Outcome, ids, formatTime(attempt.StartedAt), formatTime(attempt.FinishedAt), attempt.Error, payload)
	return err
}
func (s *Store) ListAttempts(ctx context.Context, runID string) ([]harness.Attempt, error) {
	return queryPayload[harness.Attempt](ctx, s.db, `SELECT payload FROM exploration_attempts WHERE run_id=? ORDER BY started_at,id`, runID)
}
func (s *Store) SaveObservation(ctx context.Context, observation harness.Observation) error {
	payload, _ := json.Marshal(observation)
	evidence, _ := json.Marshal(observation.Evidence)
	_, err := s.db.ExecContext(ctx, `INSERT INTO exploration_observations(id,run_id,branch_id,attempt_id,kind,target,summary,evidence,created_at,payload) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, scopedID(observation.RunID, observation.ID), observation.RunID, observation.BranchID, observation.AttemptID, observation.Kind, observation.Target, observation.Summary, evidence, formatTime(observation.CreatedAt), payload)
	return err
}
func (s *Store) ListObservations(ctx context.Context, runID string) ([]harness.Observation, error) {
	return queryPayload[harness.Observation](ctx, s.db, `SELECT payload FROM exploration_observations WHERE run_id=? ORDER BY created_at,id`, runID)
}
func (s *Store) SaveArtifact(ctx context.Context, artifact harness.Artifact) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO artifacts(run_id,id,attempt_id,session_id,name,media_type,size,sha256,redacted,created_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(run_id,id,attempt_id,name) DO UPDATE SET redacted=excluded.redacted`, artifact.RunID, artifact.ID, artifact.AttemptID, artifact.SessionID, artifact.Name, artifact.MediaType, artifact.Size, artifact.SHA256, artifact.Redacted, formatTime(artifact.CreatedAt))
	return err
}
func (s *Store) ListArtifacts(ctx context.Context, runID string) ([]harness.Artifact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT run_id,id,attempt_id,session_id,name,media_type,size,sha256,redacted,created_at FROM artifacts WHERE run_id=? ORDER BY created_at,id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []harness.Artifact
	for rows.Next() {
		var a harness.Artifact
		var tm string
		if err := rows.Scan(&a.RunID, &a.ID, &a.AttemptID, &a.SessionID, &a.Name, &a.MediaType, &a.Size, &a.SHA256, &a.Redacted, &tm); err != nil {
			return nil, err
		}
		a.CreatedAt = parseTime(tm)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) SaveKnowledge(ctx context.Context, knowledge harness.Knowledge) error {
	return s.saveKnowledgeVersion(ctx, knowledge)
}
func (s *Store) UpdateKnowledge(ctx context.Context, knowledge harness.Knowledge) error {
	if knowledge.Version <= 0 {
		knowledge.Version = 1
	}
	return s.saveKnowledgeVersion(ctx, knowledge)
}
func (s *Store) saveKnowledgeVersion(ctx context.Context, knowledge harness.Knowledge) error {
	payload, _ := json.Marshal(knowledge)
	sources, _ := json.Marshal(knowledge.Sources)
	_, err := s.db.ExecContext(ctx, `INSERT INTO knowledge(run_id,id,task_id,version,payload) VALUES(?,?,?,?,?) ON CONFLICT(run_id,id,version) DO UPDATE SET payload=excluded.payload`, knowledge.RunID, knowledge.ID, knowledge.TaskID, knowledge.Version, payload)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO knowledge_versions(run_id,knowledge_id,version,payload,created_at) VALUES(?,?,?,?,?) ON CONFLICT(run_id,knowledge_id,version) DO NOTHING`, knowledge.RunID, knowledge.ID, knowledge.Version, payload, formatTime(time.Now().UTC()))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO knowledge_sources(run_id,knowledge_id,version,source) VALUES(?,?,?,?) ON CONFLICT(run_id,knowledge_id,version,source) DO NOTHING`, knowledge.RunID, knowledge.ID, knowledge.Version, sources)
	return err
}
func (s *Store) QueryKnowledge(ctx context.Context, query harness.KnowledgeQuery) ([]harness.Knowledge, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM knowledge WHERE task_id=? ORDER BY version DESC`, query.TaskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	latest := map[string]harness.Knowledge{}
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var item harness.Knowledge
		if err := json.Unmarshal(payload, &item); err != nil {
			return nil, err
		}
		key := item.RunID + "\x00" + item.ID
		if _, ok := latest[key]; !ok {
			latest[key] = item
		}
	}
	var all []harness.Knowledge
	for _, item := range latest {
		all = append(all, item)
	}
	return harness.FilterKnowledge(all, query), nil
}

func (s *Store) SaveSweep(ctx context.Context, record harness.SweepRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sweeps(id,status,spec,result,error,created_at,finished_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,result=excluded.result,error=excluded.error,finished_at=excluded.finished_at`, record.ID, record.Status, nullableJSON(record.Spec), nullableJSON(record.Result), record.Error, formatTime(record.CreatedAt), formatTimePtr(record.FinishedAt))
	return err
}
func (s *Store) GetSweep(ctx context.Context, id string) (harness.SweepRecord, error) {
	return scanSweep(s.db.QueryRowContext(ctx, `SELECT id,status,spec,result,error,created_at,finished_at FROM sweeps WHERE id=?`, id))
}
func (s *Store) ListSweeps(ctx context.Context, limit int) ([]harness.SweepRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,status,spec,result,error,created_at,finished_at FROM sweeps ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []harness.SweepRecord
	for rows.Next() {
		record, err := scanSweep(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func scanSweep(scanner interface{ Scan(...any) error }) (harness.SweepRecord, error) {
	var record harness.SweepRecord
	var status, created string
	var spec, result []byte
	var finished sql.NullString
	if err := scanner.Scan(&record.ID, &status, &spec, &result, &record.Error, &created, &finished); err != nil {
		return record, err
	}
	record.Status = harness.Status(status)
	record.Spec = spec
	record.Result = result
	record.CreatedAt = parseTime(created)
	record.FinishedAt = parseTimePtr(finished)
	return record, nil
}
func (s *Store) MarkInterrupted(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET status=?, finished_at=? WHERE status NOT IN (?,?,?,?,?,?)`, harness.StatusInterrupted, formatTime(time.Now().UTC()), harness.StatusSucceeded, harness.StatusFailed, harness.StatusCancelled, harness.StatusTimedOut, harness.StatusInterrupted, harness.StatusStagnated)
	return err
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

var _ harness.Store = (*Store)(nil)
