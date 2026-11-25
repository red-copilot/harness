package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/red-copilot/harness/internal/lease"
)

// V5Store is the event-sourced schema 5 store. It is additive: the v8 Store
// and Open remain unchanged until the P9 cutover.
type V5Store struct {
	db       *sql.DB
	path     string
	lease    *lease.Handle
	readOnly bool

	// testAfterJournal lets fault-injection tests fail after journal inserts
	// and before the state/checkpoint update. It is nil in production.
	testAfterJournal func() error
}

// OpenV5Writer opens or creates a schema 5 database and holds the writer lease.
func OpenV5Writer(path string) (*V5Store, error) {
	if path == "" {
		path = ".harness/harness.db"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil && filepath.Dir(path) != "." {
		return nil, fmt.Errorf("create v5 database directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create v5 database file: %w", err)
	}
	_ = file.Close()
	_ = os.Chmod(path, 0o600)

	handle, err := lease.Acquire(path + ".lock")
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", v5DSN(path, false))
	if err != nil {
		_ = handle.Release()
		return nil, fmt.Errorf("open v5 sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &V5Store{db: db, path: path, lease: handle}
	if err := store.ensureSchema(); err != nil {
		_ = store.Close()
		return nil, err
	}
	if err := store.configure(); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

// OpenV5Fresh refuses to overwrite any existing database or sidecar, then
// creates a new schema 5 database.
func OpenV5Fresh(path string) (*V5Store, error) {
	if path == "" {
		path = ".harness/harness.db"
	}
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Lstat(candidate); err == nil {
			return nil, fmt.Errorf("refusing to create fresh v5 database over existing %s", candidate)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("inspect fresh v5 target %s: %w", candidate, err)
		}
	}
	return OpenV5Writer(path)
}

// OpenV5Reader opens an existing schema 5 database read-only without a lease.
func OpenV5Reader(path string) (*V5Store, error) {
	if path == "" {
		path = ".harness/harness.db"
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("open v5 reader: %w", err)
	}
	db, err := sql.Open("sqlite", v5DSN(path, true))
	if err != nil {
		return nil, fmt.Errorf("open v5 sqlite reader: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &V5Store{db: db, path: path, readOnly: true}
	if err := store.ensureSchema(); err != nil {
		_ = store.Close()
		return nil, err
	}
	if err := store.configure(); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

func v5DSN(path string, readOnly bool) string {
	escaped := strings.NewReplacer("?", "%3f", "#", "%23").Replace(path)
	if readOnly {
		return "file:" + escaped + "?mode=ro"
	}
	return "file:" + escaped + "?_txlock=immediate"
}

func (s *V5Store) configure() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pragmas := []string{"PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON"}
	if s.readOnly {
		pragmas = append(pragmas, "PRAGMA query_only=ON")
	} else {
		pragmas = append(pragmas, "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL")
	}
	for _, pragma := range pragmas {
		if _, err := s.db.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("%s: %w", pragma, err)
		}
	}
	return s.db.PingContext(ctx)
}

func (s *V5Store) ensureSchema() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var tables int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&tables); err != nil {
		return fmt.Errorf("inspect v5 schema: %w", err)
	}
	if tables == 0 {
		if s.readOnly {
			return errors.New("v5 database does not exist")
		}
		return s.initializeSchema(ctx)
	}
	var version int
	if err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_meta WHERE id=1`).Scan(&version); err != nil {
		return fmt.Errorf("database is not a v5 database; v5 does not migrate v8 in place: %w", err)
	}
	if version != v5SchemaVersion {
		return fmt.Errorf("database schema version %d is not %d; v5 does not migrate in place", version, v5SchemaVersion)
	}
	return nil
}

func (s *V5Store) initializeSchema(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin v5 schema: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range v5SchemaStatements() {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize v5 schema: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_meta(id,version,created_at) VALUES(1,?,?)`, v5SchemaVersion, time.Now().UnixNano()); err != nil {
		return fmt.Errorf("initialize v5 schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit v5 schema: %w", err)
	}
	return nil
}

// Close closes the database and releases the writer lease.
func (s *V5Store) Close() error {
	if s == nil {
		return nil
	}
	var errs []error
	if s.db != nil {
		errs = append(errs, s.db.Close())
	}
	if s.lease != nil {
		errs = append(errs, s.lease.Release())
		s.lease = nil
	}
	return errors.Join(errs...)
}

// Path returns the database path.
func (s *V5Store) Path() string { return s.path }

// ReadOnly reports whether the store is read-only.
func (s *V5Store) ReadOnly() bool { return s.readOnly }
