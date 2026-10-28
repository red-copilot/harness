package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

func TestSweepSummaryRoundTrip(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Second)
	summary := json.RawMessage(`{"id":"sweep-1","status":"running"}`)
	if err := store.SaveSweep(context.Background(), harness.SweepRecord{ID: "sweep-1", Status: harness.StatusRunning, Result: summary, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSweep(context.Background(), "sweep-1")
	if err != nil || got.Status != harness.StatusRunning || len(got.Result) == 0 {
		t.Fatalf("sweep=%+v err=%v", got, err)
	}
	items, err := store.ListSweeps(context.Background(), 10)
	if err != nil || len(items) != 1 || items[0].ID != "sweep-1" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

func TestCreateBackupAndVerifyManifest(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.db")
	source, err := Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.CreateRun(context.Background(), harness.RunRecord{ID: "legacy-run", Phase: harness.RunPhaseQueued, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(dir, "backup.db")
	manifestPath := filepath.Join(dir, "backup.json")
	manifest, err := CreateBackup(sourcePath, backupPath, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBackupManifest(manifestPath); err != nil {
		t.Fatal(err)
	}
	if manifest.RowCounts["runs"] != 1 || manifest.SHA256 == "" || manifest.SizeBytes == 0 {
		t.Fatalf("manifest = %+v", manifest)
	}
	if err := os.WriteFile(backupPath, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBackupManifest(manifestPath); err == nil {
		t.Fatal("tampered backup passed verification")
	}
}

func TestOpenFreshRefusesExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.db")
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFresh(path); err == nil {
		t.Fatal("OpenFresh overwrote an existing database")
	}
}

func TestInitializeFreshFromVerifiedBackup(t *testing.T) {
	dir := t.TempDir()
	backupPath := filepath.Join(dir, "old.db")
	legacy, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE schema_meta(version INTEGER NOT NULL)`,
		`INSERT INTO schema_meta(version) VALUES(1)`,
		`CREATE TABLE runs(id TEXT PRIMARY KEY)`,
		`INSERT INTO runs(id) VALUES('legacy-run')`,
		`CREATE TABLE events(seq INTEGER PRIMARY KEY, run_id TEXT NOT NULL)`,
		`INSERT INTO events(run_id) VALUES('legacy-run')`,
	} {
		if _, err := legacy.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	version := 1
	manifest := BackupManifest{
		Source: "old.db", Backup: backupPath, CreatedAt: time.Now().UTC().Format(time.RFC3339),
		SHA256: sha256HexFile(t, backupPath), SizeBytes: fileSize(t, backupPath),
		SchemaVersion: &version, RowCounts: map[string]int64{"events": 1, "runs": 1, "schema_meta": 1},
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "new.db")
	store, err := InitializeFreshFromVerifiedBackup(manifestPath, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var gotVersion int
	if err := store.db.QueryRow(`SELECT version FROM schema_meta`).Scan(&gotVersion); err != nil {
		t.Fatal(err)
	}
	if gotVersion != 7 {
		t.Fatalf("version=%d want 7", gotVersion)
	}
	var runs int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 0 {
		t.Fatalf("fresh database contains %d legacy runs", runs)
	}
}

func TestVerifyBackupManifestRejectsRowCountMismatch(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "source.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_meta(version INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_meta(version) VALUES(6)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE runs(id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO runs(id) VALUES('one')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	version := 6
	manifest := BackupManifest{
		Source: dbPath, Backup: dbPath, CreatedAt: time.Now().UTC().Format(time.RFC3339),
		SHA256: sha256HexFile(t, dbPath), SizeBytes: fileSize(t, dbPath), SchemaVersion: &version,
		RowCounts: map[string]int64{"runs": 2, "schema_meta": 1},
	}
	manifestPath := filepath.Join(dir, "bad.json")
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBackupManifest(manifestPath); err == nil {
		t.Fatal("row count mismatch passed verification")
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func sha256HexFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
