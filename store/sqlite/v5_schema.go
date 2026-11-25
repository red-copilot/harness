package sqlite

const v5SchemaVersion = 5

// v5SchemaStatements are the schema 5 tables, triggers and indexes. Tables are
// created before triggers so trigger bodies can reference every table.
func v5SchemaStatements() []string {
	return []string{
		`CREATE TABLE schema_meta (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			version INTEGER NOT NULL CHECK (version = 5),
			created_at INTEGER NOT NULL
		) STRICT`,
		`CREATE TABLE run_manifests (
			run_id TEXT PRIMARY KEY,
			descriptor BLOB NOT NULL,
			descriptor_sha256 TEXT NOT NULL CHECK (length(descriptor_sha256) = 64),
			created_at INTEGER NOT NULL
		) STRICT, WITHOUT ROWID`,
		`CREATE TABLE run_journal (
			run_id TEXT NOT NULL REFERENCES run_manifests(run_id),
			seq INTEGER NOT NULL CHECK (seq > 0),
			type TEXT NOT NULL,
			schema_version INTEGER NOT NULL CHECK (schema_version > 0),
			at INTEGER NOT NULL,
			payload BLOB NOT NULL,
			prev_hash TEXT NOT NULL CHECK (length(prev_hash) = 64),
			hash TEXT NOT NULL CHECK (length(hash) = 64),
			PRIMARY KEY (run_id, seq)
		) STRICT, WITHOUT ROWID`,
		`CREATE TABLE run_state (
			run_id TEXT PRIMARY KEY REFERENCES run_manifests(run_id),
			phase TEXT NOT NULL CHECK (phase IN ('queued','provisioning','running','verifying','terminal')),
			outcome TEXT NOT NULL DEFAULT '' CHECK (outcome IN ('','succeeded','failed','cancelled','timed_out','interrupted','stagnated')),
			head_seq INTEGER NOT NULL CHECK (head_seq > 0),
			head_hash TEXT NOT NULL CHECK (length(head_hash) = 64),
			board_revision INTEGER NOT NULL DEFAULT 0 CHECK (board_revision >= 0),
			usage BLOB,
			goal_candidate INTEGER NOT NULL DEFAULT 0 CHECK (goal_candidate IN (0,1)),
			non_growth_rounds INTEGER NOT NULL DEFAULT 0 CHECK (non_growth_rounds >= 0),
			reason_failures INTEGER NOT NULL DEFAULT 0 CHECK (reason_failures >= 0),
			stagnated INTEGER NOT NULL DEFAULT 0 CHECK (stagnated IN (0,1)),
			started_at INTEGER,
			finished_at INTEGER,
			error TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL,
			FOREIGN KEY (run_id, head_seq) REFERENCES run_journal(run_id, seq),
			CHECK ((phase = 'terminal' AND outcome <> '') OR (phase <> 'terminal' AND outcome = ''))
		) STRICT, WITHOUT ROWID`,
		`CREATE TABLE run_finalization (
			run_id TEXT PRIMARY KEY REFERENCES run_manifests(run_id),
			outcome TEXT NOT NULL CHECK (outcome IN ('succeeded','failed','cancelled','timed_out','interrupted','stagnated')),
			journal_seq INTEGER NOT NULL CHECK (journal_seq > 0),
			verdict_sha256 TEXT CHECK (verdict_sha256 IS NULL OR length(verdict_sha256) = 64),
			finalized_at INTEGER NOT NULL,
			FOREIGN KEY (run_id, journal_seq) REFERENCES run_journal(run_id, seq)
		) STRICT, WITHOUT ROWID`,
		`CREATE TABLE verdicts (
			run_id TEXT PRIMARY KEY REFERENCES run_manifests(run_id),
			success INTEGER NOT NULL CHECK (success IN (0,1)),
			score REAL NOT NULL DEFAULT 0,
			message TEXT NOT NULL DEFAULT '',
			evidence BLOB,
			verifier_id TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL
		) STRICT, WITHOUT ROWID`,
		`CREATE TABLE run_checkpoints (
			run_id TEXT NOT NULL,
			seq INTEGER NOT NULL CHECK (seq > 0),
			state_version INTEGER NOT NULL CHECK (state_version > 0),
			state BLOB NOT NULL,
			state_sha256 TEXT NOT NULL CHECK (length(state_sha256) = 64),
			head_hash TEXT NOT NULL CHECK (length(head_hash) = 64),
			created_at INTEGER NOT NULL,
			PRIMARY KEY (run_id, seq),
			FOREIGN KEY (run_id, seq) REFERENCES run_journal(run_id, seq)
		) STRICT, WITHOUT ROWID`,
		`CREATE TABLE artifact_blobs (
			sha256 TEXT PRIMARY KEY CHECK (length(sha256) = 64),
			size INTEGER NOT NULL CHECK (size >= 0),
			data BLOB NOT NULL,
			created_at INTEGER NOT NULL
		) STRICT, WITHOUT ROWID`,
		`CREATE TABLE run_logs (
			run_id TEXT NOT NULL REFERENCES run_manifests(run_id),
			seq INTEGER NOT NULL CHECK (seq > 0),
			session_id TEXT NOT NULL DEFAULT '',
			stream TEXT NOT NULL,
			at INTEGER NOT NULL,
			text TEXT NOT NULL,
			PRIMARY KEY (run_id, seq)
		) STRICT, WITHOUT ROWID`,
		`CREATE TABLE rm_claims (
			run_id TEXT NOT NULL, id TEXT NOT NULL, seq INTEGER NOT NULL,
			kind TEXT NOT NULL, payload BLOB NOT NULL,
			PRIMARY KEY (run_id, id)
		) STRICT, WITHOUT ROWID`,
		`CREATE TABLE rm_intents (
			run_id TEXT NOT NULL, id TEXT NOT NULL, seq INTEGER NOT NULL,
			state TEXT NOT NULL, payload BLOB NOT NULL,
			PRIMARY KEY (run_id, id)
		) STRICT, WITHOUT ROWID`,
		`CREATE TABLE rm_attempts (
			run_id TEXT NOT NULL, id TEXT NOT NULL, seq INTEGER NOT NULL,
			intent_id TEXT NOT NULL, phase TEXT NOT NULL, payload BLOB NOT NULL,
			PRIMARY KEY (run_id, id)
		) STRICT, WITHOUT ROWID`,
		`CREATE TABLE rm_observations (
			run_id TEXT NOT NULL, id TEXT NOT NULL, seq INTEGER NOT NULL,
			intent_id TEXT NOT NULL, attempt_id TEXT NOT NULL, target TEXT NOT NULL,
			payload BLOB NOT NULL,
			PRIMARY KEY (run_id, id)
		) STRICT, WITHOUT ROWID`,
		`CREATE TABLE rm_artifacts (
			run_id TEXT NOT NULL, artifact_id TEXT NOT NULL,
			attempt_id TEXT NOT NULL, name TEXT NOT NULL,
			sha256 TEXT NOT NULL CHECK (length(sha256) = 64),
			size INTEGER NOT NULL, media_type TEXT NOT NULL,
			redacted INTEGER NOT NULL DEFAULT 0 CHECK (redacted IN (0,1)),
			payload BLOB NOT NULL,
			PRIMARY KEY (run_id, artifact_id, attempt_id, name)
		) STRICT, WITHOUT ROWID`,

		`CREATE TRIGGER run_manifests_no_update BEFORE UPDATE ON run_manifests
		BEGIN SELECT RAISE(ABORT, 'manifest is immutable'); END`,
		`CREATE TRIGGER run_manifests_no_delete BEFORE DELETE ON run_manifests
		BEGIN SELECT RAISE(ABORT, 'manifest is immutable'); END`,
		`CREATE TRIGGER run_journal_no_update BEFORE UPDATE ON run_journal
		BEGIN SELECT RAISE(ABORT, 'journal is append-only'); END`,
		`CREATE TRIGGER run_journal_no_delete BEFORE DELETE ON run_journal
		BEGIN SELECT RAISE(ABORT, 'journal is append-only'); END`,
		`CREATE TRIGGER run_journal_stop_after_terminal BEFORE INSERT ON run_journal
		WHEN EXISTS (SELECT 1 FROM run_state WHERE run_id = NEW.run_id AND phase = 'terminal')
		BEGIN SELECT RAISE(ABORT, 'run is terminal'); END`,
		`CREATE TRIGGER run_state_terminal_requires_finalization BEFORE UPDATE OF phase ON run_state
		WHEN NEW.phase = 'terminal' AND NOT EXISTS (
			SELECT 1 FROM run_finalization f
			WHERE f.run_id = NEW.run_id AND f.outcome = NEW.outcome
		)
		BEGIN SELECT RAISE(ABORT, 'terminal requires matching finalization'); END`,
		`CREATE TRIGGER run_state_terminal_immutable BEFORE UPDATE ON run_state
		WHEN OLD.phase = 'terminal'
		BEGIN SELECT RAISE(ABORT, 'terminal state is immutable'); END`,
		`CREATE TRIGGER run_state_no_delete BEFORE DELETE ON run_state
		BEGIN SELECT RAISE(ABORT, 'run state is not deletable'); END`,
		`CREATE TRIGGER run_finalization_success_requires_verdict BEFORE INSERT ON run_finalization
		WHEN NEW.outcome = 'succeeded' AND NOT EXISTS (
			SELECT 1 FROM verdicts v WHERE v.run_id = NEW.run_id AND v.success = 1
		)
		BEGIN SELECT RAISE(ABORT, 'successful finalization requires successful verdict'); END`,
		`CREATE TRIGGER run_finalization_no_update BEFORE UPDATE ON run_finalization
		BEGIN SELECT RAISE(ABORT, 'finalization is immutable'); END`,
		`CREATE TRIGGER run_finalization_no_delete BEFORE DELETE ON run_finalization
		BEGIN SELECT RAISE(ABORT, 'finalization is immutable'); END`,
		`CREATE TRIGGER verdicts_require_verifying BEFORE INSERT ON verdicts
		WHEN NOT EXISTS (
			SELECT 1 FROM run_state s WHERE s.run_id = NEW.run_id AND s.phase = 'verifying'
		)
		BEGIN SELECT RAISE(ABORT, 'verdict requires verifying phase'); END`,
		`CREATE TRIGGER verdicts_no_update BEFORE UPDATE ON verdicts
		BEGIN SELECT RAISE(ABORT, 'verdict is immutable'); END`,
		`CREATE TRIGGER verdicts_no_delete BEFORE DELETE ON verdicts
		BEGIN SELECT RAISE(ABORT, 'verdict is immutable'); END`,
		`CREATE TRIGGER artifact_blobs_no_update BEFORE UPDATE ON artifact_blobs
		BEGIN SELECT RAISE(ABORT, 'artifact blobs are immutable'); END`,
		`CREATE TRIGGER artifact_blobs_no_delete BEFORE DELETE ON artifact_blobs
		BEGIN SELECT RAISE(ABORT, 'artifact blobs are immutable'); END`,
		`CREATE TRIGGER run_logs_no_update BEFORE UPDATE ON run_logs
		BEGIN SELECT RAISE(ABORT, 'logs are append-only'); END`,
		`CREATE TRIGGER run_logs_no_delete BEFORE DELETE ON run_logs
		BEGIN SELECT RAISE(ABORT, 'logs are append-only'); END`,
		`CREATE TRIGGER run_logs_stop_after_terminal BEFORE INSERT ON run_logs
		WHEN EXISTS (SELECT 1 FROM run_state WHERE run_id = NEW.run_id AND phase = 'terminal')
		BEGIN SELECT RAISE(ABORT, 'run is terminal'); END`,

		`CREATE INDEX run_state_phase ON run_state(phase)`,
		`CREATE INDEX run_logs_run_seq ON run_logs(run_id, seq)`,
		`CREATE INDEX rm_claims_seq ON rm_claims(run_id, seq)`,
		`CREATE INDEX rm_intents_seq ON rm_intents(run_id, seq)`,
		`CREATE INDEX rm_attempts_seq ON rm_attempts(run_id, seq)`,
		`CREATE INDEX rm_observations_seq ON rm_observations(run_id, seq)`,
		`CREATE INDEX rm_artifacts_run ON rm_artifacts(run_id, attempt_id, name)`,
	}
}
