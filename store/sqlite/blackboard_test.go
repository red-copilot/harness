package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

func TestBlackboardRoundTrip(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	fact := harness.Fact{ID: "fact-1", RunID: "run-1", TaskID: "task-1", Kind: harness.FactFinding, Description: "service exposed", ObservationIDs: []string{"obs-1"}, ArtifactIDs: []string{"artifact-1"}, CreatedAt: now}
	intent := harness.Intent{ID: "intent-1", RunID: "run-1", Kind: harness.IntentExplore, FromFactIDs: []string{"fact-1"}, Description: "probe the service", State: harness.IntentClaimed, Slot: 1, ExecutorID: "executor-1", CreatedAt: now}
	hint := harness.Hint{ID: "hint-1", RunID: "run-1", Content: "try the admin path", Creator: "operator", CreatedAt: now}
	attempt := harness.IntentAttempt{ID: "attempt-1", RunID: "run-1", IntentID: intent.ID, Slot: 1, SessionID: "executor-1", Phase: harness.IntentAttemptRunning, StartedAt: now}
	observation := harness.Observation{ID: "obs-1", RunID: "run-1", AttemptID: "attempt-1", Kind: "http.response", Summary: "200 OK", Target: "target-1", CreatedAt: now}
	for name, fn := range map[string]func() error{
		"fact":        func() error { return store.SaveFact(ctx, fact) },
		"intent":      func() error { return store.SaveIntent(ctx, intent) },
		"hint":        func() error { return store.SaveHint(ctx, hint) },
		"attempt":     func() error { return store.SaveIntentAttempt(ctx, attempt) },
		"observation": func() error { return store.SaveBlackboardObservation(ctx, observation) },
	} {
		if err := fn(); err != nil {
			t.Fatalf("save %s: %v", name, err)
		}
	}
	if got, err := store.ListFacts(ctx, "run-1"); err != nil || len(got) != 1 || got[0].ID != fact.ID {
		t.Fatalf("facts = %+v, err = %v", got, err)
	}
	if got, err := store.ListIntents(ctx, "run-1"); err != nil || len(got) != 1 || got[0].Description != intent.Description {
		t.Fatalf("intents = %+v, err = %v", got, err)
	}
	if got, err := store.ListHints(ctx, "run-1"); err != nil || len(got) != 1 || got[0].Content != hint.Content {
		t.Fatalf("hints = %+v, err = %v", got, err)
	}
	if got, err := store.ListIntentAttempts(ctx, "run-1"); err != nil || len(got) != 1 || got[0].IntentID != intent.ID {
		t.Fatalf("attempts = %+v, err = %v", got, err)
	}
	if got, err := store.ListBlackboardObservations(ctx, "run-1"); err != nil || len(got) != 1 || got[0].ID != observation.ID {
		t.Fatalf("observations = %+v, err = %v", got, err)
	}
}

func TestBlackboardRunIsolation(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	for _, runID := range []string{"run-1", "run-2"} {
		intent := harness.Intent{ID: "intent-1", RunID: runID, Kind: harness.IntentExplore, FromFactIDs: []string{"origin"}, Description: runID, State: harness.IntentOpen, CreatedAt: now}
		if err := store.SaveIntent(ctx, intent); err != nil {
			t.Fatal(err)
		}
	}
	for _, runID := range []string{"run-1", "run-2"} {
		items, err := store.ListIntents(ctx, runID)
		if err != nil || len(items) != 1 || items[0].Description != runID {
			t.Fatalf("run %s intents = %+v, err = %v", runID, items, err)
		}
	}
}

func TestBlackboardIntentSourcesAreOrdered(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	intent := harness.Intent{ID: "intent-1", RunID: "run-1", Kind: harness.IntentExplore, FromFactIDs: []string{"fact-2", "fact-1", "fact-3"}, Description: "combine facts", State: harness.IntentOpen}
	if err := store.SaveIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListIntents(ctx, "run-1")
	if err != nil || len(items) != 1 {
		t.Fatalf("intents = %+v, err = %v", items, err)
	}
	want := []string{"fact-2", "fact-1", "fact-3"}
	for i := range want {
		if items[0].FromFactIDs[i] != want[i] {
			t.Fatalf("from facts = %v, want %v", items[0].FromFactIDs, want)
		}
	}
}

func TestBlackboardOpenRejectsLegacySchemaWithoutChangingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE schema_meta (version INTEGER NOT NULL)`,
		`INSERT INTO schema_meta(version) VALUES (4)`,
		`CREATE TABLE events (seq INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL, session_id TEXT NOT NULL DEFAULT '', source TEXT NOT NULL, type TEXT NOT NULL, time TEXT NOT NULL, data BLOB, raw BLOB, schema_version INTEGER NOT NULL DEFAULT 1)`,
		`CREATE TABLE logs (seq INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL, session_id TEXT NOT NULL DEFAULT '', stream TEXT NOT NULL, time TEXT NOT NULL, text TEXT NOT NULL)`,
		`INSERT INTO events(run_id,source,type,time,data) VALUES('legacy','test','legacy.event','2026-01-01T00:00:00Z','{}')`,
		`INSERT INTO logs(run_id,stream,time,text) VALUES('legacy','stdout','2026-01-01T00:00:00Z','kept')`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("setup legacy schema: %v", err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	if store, err := Open(path); err == nil {
		_ = store.Close()
		t.Fatal("legacy schema was upgraded in place")
	}
	raw, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var version, events, logs int
	if err := raw.QueryRow(`SELECT version FROM schema_meta`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`SELECT COUNT(*) FROM logs`).Scan(&logs); err != nil {
		t.Fatal(err)
	}
	if version != 4 || events != 1 || logs != 1 {
		t.Fatalf("legacy database changed: version=%d events=%d logs=%d", version, events, logs)
	}
}
