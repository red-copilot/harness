package sqlite

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/red-copilot/harness/core"
)

func TestEventHashChainVerifiesAndIsAppendOnly(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.CreateRun(ctx, core.RunRecord{ID: "run-1", Phase: core.RunPhaseQueued, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := store.AppendEvent(ctx, core.Event{RunID: "run-1", Source: "test", Type: core.EventAgent, Time: time.Now().UTC(), Data: json.RawMessage(`{"n":1}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.VerifyEventChain(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE events SET source='tampered' WHERE seq=1`); err == nil {
		t.Fatal("event update succeeded despite append-only trigger")
	}
}

func TestArtifactsAreStoredInsideSQLite(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	artifact, err := store.Put(context.Background(), core.ArtifactInput{RunID: "run-1", AttemptID: "attempt-1", Name: "proof.txt", MediaType: "text/plain", Size: 5, Content: strings.NewReader("proof")})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := store.Open(context.Background(), artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(data) != "proof" {
		t.Fatalf("artifact data=%q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "artifacts")); !os.IsNotExist(err) {
		t.Fatal("legacy artifact directory was created")
	}
}
