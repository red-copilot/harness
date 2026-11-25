package sqlite

import (
	"path/filepath"
	"testing"

	"github.com/red-copilot/harness/engine"
	"github.com/red-copilot/harness/engine/porttest"
)

func TestV5StoreConformance(t *testing.T) {
	porttest.Run(t, func(t *testing.T) engine.V5Store {
		t.Helper()
		store, err := OpenV5Fresh(filepath.Join(t.TempDir(), "harness.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store
	})
}
