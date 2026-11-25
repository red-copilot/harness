package core

import (
	"testing"
	"time"
)

const testManifestDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestNewRecordDecodeAndChain(t *testing.T) {
	at := time.Unix(1, 0).UTC()
	first, err := NewRecord("run-1", 1, RecordRunCreated, at, RunCreatedRecord{
		TaskID: "task-1", TargetID: "target-1", ManifestDigest: testManifestDigest,
		ReasonerID: "reasoner", ExecutorID: "executor", PolicyDigest: "policy",
	}, testManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewRecord("run-1", 2, RecordRunPhase, at, RunPhaseRecord{From: PhaseQueued, To: PhaseProvisioning, At: at}, first.Hash)
	if err != nil {
		t.Fatal(err)
	}
	records := []Record{first, second}
	if err := VerifyRecordChain(testManifestDigest, records); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeRecord(first)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded.(RunCreatedRecord); !ok {
		t.Fatalf("decoded %T", decoded)
	}
	if first.Hash == "" || len(first.Hash) != 64 {
		t.Fatalf("hash = %q", first.Hash)
	}
}

func TestVerifyRecordChainRejectsTampering(t *testing.T) {
	at := time.Unix(1, 0).UTC()
	first, err := NewRecord("run-1", 1, RecordRunCreated, at, RunCreatedRecord{TaskID: "t"}, testManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	tampered := first
	tampered.Payload = []byte(`{"task_id":"other"}`)
	if err := VerifyRecordChain(testManifestDigest, []Record{tampered}); err == nil {
		t.Fatal("tampered payload accepted")
	}
	badPrev := first
	badPrev.PrevHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := VerifyRecordChain(testManifestDigest, []Record{badPrev}); err == nil {
		t.Fatal("broken prev_hash accepted")
	}
	badVersion := first
	badVersion.SchemaVersion = 2
	if err := VerifyRecordChain(testManifestDigest, []Record{badVersion}); err == nil {
		t.Fatal("unknown schema version accepted")
	}
	if err := VerifyRecordChain("short", recordsOrNil(first)); err == nil {
		t.Fatal("short manifest digest accepted")
	}
}

func recordsOrNil(r Record) []Record { return []Record{r} }

func TestNewRecordRejectsInvalidInput(t *testing.T) {
	at := time.Unix(1, 0).UTC()
	cases := []struct {
		name    string
		typ     RecordType
		payload any
		prev    string
	}{
		{"unknown type", RecordType("nope"), RunCreatedRecord{}, testManifestDigest},
		{"wrong payload", RecordRunCreated, RunPhaseRecord{}, testManifestDigest},
		{"short prev", RecordRunCreated, RunCreatedRecord{}, "short"},
		{"empty run", RecordRunCreated, RunCreatedRecord{}, testManifestDigest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runID := "run-1"
			if tc.name == "empty run" {
				runID = ""
			}
			if _, err := NewRecord(runID, 1, tc.typ, at, tc.payload, tc.prev); err == nil {
				t.Fatalf("record accepted: %+v", tc)
			}
		})
	}
	if _, err := NewRecord("run-1", 0, RecordRunCreated, at, RunCreatedRecord{}, testManifestDigest); err == nil {
		t.Fatal("zero sequence accepted")
	}
	if _, err := NewRecord("run-1", 1, RecordRunCreated, time.Time{}, RunCreatedRecord{}, testManifestDigest); err == nil {
		t.Fatal("zero time accepted")
	}
}

func TestRecordTypesAndVersions(t *testing.T) {
	types := RecordTypes()
	if len(types) == 0 {
		t.Fatal("no record types registered")
	}
	for i := 1; i < len(types); i++ {
		if types[i-1] >= types[i] {
			t.Fatalf("record types are not sorted: %v", types)
		}
	}
	version, ok := RecordSchemaVersion(RecordClaimCreated)
	if !ok || version != 1 {
		t.Fatalf("claim.created version = %d, %v", version, ok)
	}
	if _, ok := RecordSchemaVersion(RecordType("nope")); ok {
		t.Fatal("unknown type reported a version")
	}
}

func FuzzDecodeRecord(f *testing.F) {
	f.Add([]byte(`{}`), "run.created", 1)
	f.Add([]byte(``), "nope", 0)
	f.Fuzz(func(t *testing.T, payload []byte, typ string, version int) {
		_, _ = DecodeRecord(Record{Type: RecordType(typ), SchemaVersion: version, Payload: payload})
	})
}
