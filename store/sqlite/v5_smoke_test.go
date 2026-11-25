package sqlite

import (
	"path/filepath"
	"testing"
)

func TestV5OpenFreshSmoke(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.db")
	store, err := OpenV5Fresh(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var version int
	if err := store.db.QueryRow(`SELECT version FROM schema_meta WHERE id=1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 5 {
		t.Fatalf("schema version = %d, want 5", version)
	}
}
