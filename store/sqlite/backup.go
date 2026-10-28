package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BackupManifest records the operator-visible evidence that a pre-cutover
// SQLite database was copied before the v2 fresh-database boundary.
type BackupManifest struct {
	Source        string           `json:"source"`
	Backup        string           `json:"backup"`
	CreatedAt     string           `json:"created_at"`
	SHA256        string           `json:"sha256"`
	SizeBytes     int64            `json:"size_bytes"`
	SchemaVersion *int             `json:"schema_version,omitempty"`
	RowCounts     map[string]int64 `json:"row_counts"`
}

// VerifyBackupManifest verifies that the manifest points to a regular backup
// whose bytes and size still match the recorded values. It does not open or
// mutate the backup.
func VerifyBackupManifest(path string) (BackupManifest, error) {
	var manifest BackupManifest
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest, fmt.Errorf("read backup manifest: %w", err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, fmt.Errorf("decode backup manifest: %w", err)
	}
	if manifest.Backup == "" || manifest.SHA256 == "" || manifest.SizeBytes <= 0 {
		return manifest, fmt.Errorf("backup manifest is incomplete")
	}
	info, err := os.Lstat(manifest.Backup)
	if err != nil {
		return manifest, fmt.Errorf("inspect backup: %w", err)
	}
	if !info.Mode().IsRegular() {
		return manifest, fmt.Errorf("backup is not a regular file")
	}
	if info.Size() != manifest.SizeBytes {
		return manifest, fmt.Errorf("backup size mismatch: manifest=%d actual=%d", manifest.SizeBytes, info.Size())
	}
	file, err := os.Open(manifest.Backup)
	if err != nil {
		return manifest, fmt.Errorf("open backup: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return manifest, fmt.Errorf("hash backup: %w", err)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != manifest.SHA256 {
		return manifest, fmt.Errorf("backup sha256 mismatch: manifest=%s actual=%s", manifest.SHA256, actual)
	}
	backupDB, err := sql.Open("sqlite", manifest.Backup)
	if err != nil {
		return manifest, fmt.Errorf("open verified backup: %w", err)
	}
	defer backupDB.Close()
	actualCounts, actualVersion, err := inspectDatabase(backupDB)
	if err != nil {
		return manifest, err
	}
	if manifest.RowCounts == nil {
		return manifest, fmt.Errorf("backup manifest has no row counts")
	}
	for table, expected := range manifest.RowCounts {
		if actualCounts[table] != expected {
			return manifest, fmt.Errorf("backup row count mismatch for %s: manifest=%d actual=%d", table, expected, actualCounts[table])
		}
	}
	if manifest.SchemaVersion != nil && actualVersion != nil && *manifest.SchemaVersion != *actualVersion {
		return manifest, fmt.Errorf("backup schema version mismatch: manifest=%d actual=%d", *manifest.SchemaVersion, *actualVersion)
	}
	return manifest, nil
}

// InitializeFreshFromVerifiedBackup is the production cutover helper. It
// verifies the pre-cutover backup first, then creates a brand-new v2 database
// without copying old Runs into the writable store.
func InitializeFreshFromVerifiedBackup(manifestPath, databasePath string) (*Store, error) {
	if _, err := VerifyBackupManifest(manifestPath); err != nil {
		return nil, err
	}
	dir := filepath.Dir(databasePath)
	if err := os.MkdirAll(dir, 0o700); err != nil && dir != "." {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	return OpenFresh(databasePath)
}

// CreateBackup makes a consistent SQLite backup and writes a manifest suitable
// for VerifyBackupManifest. It never opens or mutates the source through the
// canonical writer.
func CreateBackup(sourcePath, backupPath, manifestPath string) (BackupManifest, error) {
	var manifest BackupManifest
	if sourcePath == "" || backupPath == "" || manifestPath == "" {
		return manifest, fmt.Errorf("source, backup and manifest paths are required")
	}
	if filepath.Clean(sourcePath) == filepath.Clean(backupPath) {
		return manifest, fmt.Errorf("backup path must differ from source")
	}
	if err := os.MkdirAll(filepath.Dir(backupPath), 0o700); err != nil && filepath.Dir(backupPath) != "." {
		return manifest, fmt.Errorf("create backup directory: %w", err)
	}
	if _, err := os.Lstat(backupPath); err == nil {
		return manifest, fmt.Errorf("refusing to overwrite existing backup %s", backupPath)
	} else if !os.IsNotExist(err) {
		return manifest, err
	}
	src, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		return manifest, fmt.Errorf("open source sqlite: %w", err)
	}
	defer src.Close()
	escaped := strings.ReplaceAll(backupPath, "'", "''")
	if _, err := src.ExecContext(context.Background(), `VACUUM INTO '`+escaped+`'`); err != nil {
		return manifest, fmt.Errorf("copy sqlite: %w", err)
	}
	hash, size, err := fileDigest(backupPath)
	if err != nil {
		return manifest, err
	}
	backupDB, err := sql.Open("sqlite", backupPath)
	if err != nil {
		return manifest, fmt.Errorf("open backup for manifest: %w", err)
	}
	counts, version, inspectErr := inspectDatabase(backupDB)
	closeErr := backupDB.Close()
	if inspectErr != nil {
		return manifest, inspectErr
	}
	if closeErr != nil {
		return manifest, closeErr
	}
	manifest = BackupManifest{
		Source: filepath.Clean(sourcePath), Backup: filepath.Clean(backupPath),
		CreatedAt: time.Now().UTC().Format(time.RFC3339), SHA256: hash, SizeBytes: size,
		SchemaVersion: version, RowCounts: counts,
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return manifest, err
	}
	data = append(data, '\n')
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		return manifest, fmt.Errorf("write backup manifest: %w", err)
	}
	return manifest, nil
}

func inspectDatabase(db *sql.DB) (map[string]int64, *int, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, nil, fmt.Errorf("list backup tables: %w", err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			rows.Close()
			return nil, nil, err
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, nil, err
	}
	counts := make(map[string]int64, len(tables))
	for _, table := range tables {
		var count int64
		if err := db.QueryRow(`SELECT COUNT(*) FROM "` + strings.ReplaceAll(table, `"`, `""`) + `"`).Scan(&count); err != nil {
			return nil, nil, fmt.Errorf("count backup table %s: %w", table, err)
		}
		counts[table] = count
	}
	var version int
	if err := db.QueryRow(`SELECT version FROM schema_meta LIMIT 1`).Scan(&version); err != nil {
		return nil, nil, fmt.Errorf("read backup schema version: %w", err)
	}
	return counts, &version, nil
}

func fileDigest(path string) (string, int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), info.Size(), nil
}
