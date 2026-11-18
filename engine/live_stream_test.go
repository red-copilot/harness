package engine

import (
	"encoding/json"
	"testing"

	"github.com/red-copilot/harness/core"
)

// TestLiveStreamDropsAreCounted pins the lossy-live-stream diagnostic: when
// the consumer does not drain the channel, publishing must not block, must drop
// from the stream, and must count the loss so the terminal record can report it.
func TestLiveStreamDropsAreCounted(t *testing.T) {
	run, store := newFinalizationTestRun(t)
	controller := run.controller

	// Fill the live buffer completely so every publish drops.
	for i := 0; i < cap(controller.events); i++ {
		if err := controller.lockCanonicalWrite(); err != nil {
			t.Fatal(err)
		}
		controller.publishEvent(core.Event{Type: core.EventRunPhase})
		controller.eventMu.Unlock()
	}
	if controller.droppedEvents != 0 {
		t.Fatalf("dropped = %d while buffer had room", controller.droppedEvents)
	}
	if err := controller.lockCanonicalWrite(); err != nil {
		t.Fatal(err)
	}
	controller.publishEvent(core.Event{Type: core.EventRunPhase})
	controller.eventMu.Unlock()
	if controller.droppedEvents != 1 {
		t.Fatalf("dropped = %d, want 1 after a full buffer", controller.droppedEvents)
	}

	// Finalization must record the loss in the terminal metadata.
	controller.finishExecution(core.RunOutcomeFailed, nil, nil)
	record := store.runs["run-1"]
	if len(record.Metadata) == 0 {
		t.Fatal("terminal record metadata missing dropped-live-events diagnostic")
	}
	var metadata struct {
		Dropped uint64 `json:"dropped_live_events"`
	}
	if err := json.Unmarshal(record.Metadata, &metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if metadata.Dropped != 1 {
		t.Fatalf("recorded dropped = %d, want 1", metadata.Dropped)
	}
}

// TestLiveStreamPublishesWhenDrained is the negative control: with a drained
// channel nothing is dropped.
func TestLiveStreamPublishesWhenDrained(t *testing.T) {
	run, store := newFinalizationTestRun(t)
	controller := run.controller
	go func() {
		for range controller.events {
		}
	}()
	if err := controller.lockCanonicalWrite(); err != nil {
		t.Fatal(err)
	}
	controller.publishEvent(core.Event{Type: core.EventRunPhase})
	controller.eventMu.Unlock()
	controller.finishExecution(core.RunOutcomeFailed, nil, nil)
	if record := store.runs["run-1"]; len(record.Metadata) != 0 {
		t.Fatalf("metadata = %s, want none when nothing was dropped", record.Metadata)
	}
}
