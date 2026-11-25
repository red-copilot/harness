package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/red-copilot/harness/internal/lease"
)

// CutoverV5 is the one-way v8 -> v5 operator path. It verifies the v8 backup
// manifest, archives the live v8 database and sidecars, and creates a fresh
// schema 5 database at the same path. The archive is never deleted.
func CutoverV5(databasePath, manifestPath, archiveDir string) (*V5Store, error) {
	if databasePath == "" || manifestPath == "" || archiveDir == "" {
		return nil, errors.New("database, manifest and archive paths are required")
	}
	manifest, err := VerifyBackupManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	if filepath.Clean(manifest.Source) != filepath.Clean(databasePath) {
		return nil, fmt.Errorf("backup manifest source %q does not match database %q", manifest.Source, databasePath)
	}
	if err := inspectV8Database(databasePath); err != nil {
		return nil, err
	}

	handle, err := lease.Acquire(databasePath + ".lock")
	if err != nil {
		return nil, err
	}
	archived, archiveErr := archiveDatabase(databasePath, archiveDir)
	if releaseErr := handle.Release(); releaseErr != nil && archiveErr == nil {
		archiveErr = releaseErr
	}
	if archiveErr != nil {
		return nil, archiveErr
	}

	store, err := OpenV5Fresh(databasePath)
	if err != nil {
		return nil, fmt.Errorf("create fresh v5 database after archiving %v: %w", archived, err)
	}
	record := map[string]any{
		"schema_version":    v5SchemaVersion,
		"source":            manifest.Source,
		"backup":            manifest.Backup,
		"backup_sha256":     manifest.SHA256,
		"backup_size_bytes": manifest.SizeBytes,
		"archived":          archived,
		"cutover_at":        time.Now().UTC().Format(time.RFC3339Nano),
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(archiveDir, "cutover.json"), data, 0o600); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("write cutover record: %w", err)
	}
	return store, nil
}

func inspectV8Database(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("inspect live database: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open live database: %w", err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow(`SELECT version FROM schema_meta LIMIT 1`).Scan(&version); err != nil {
		return fmt.Errorf("live database is not a readable v8 database: %w", err)
	}
	if version != 8 {
		return fmt.Errorf("live database schema version is %d, want 8", version)
	}
	return nil
}

func archiveDatabase(databasePath, archiveDir string) ([]string, error) {
	if err := os.MkdirAll(archiveDir, 0o700); err != nil {
		return nil, fmt.Errorf("create archive directory: %w", err)
	}
	var archived []string
	for _, suffix := range []string{"", "-wal", "-shm"} {
		source := databasePath + suffix
		if _, err := os.Stat(source); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("inspect %s: %w", source, err)
		}
		destination := filepath.Join(archiveDir, filepath.Base(source))
		if _, err := os.Stat(destination); err == nil {
			return nil, fmt.Errorf("archive target already exists: %s", destination)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("inspect archive target %s: %w", destination, err)
		}
		if err := os.Rename(source, destination); err != nil {
			return nil, fmt.Errorf("archive %s: %w", source, err)
		}
		archived = append(archived, destination)
	}
	return archived, nil
}
