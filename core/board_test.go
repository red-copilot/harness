package core

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

type boardRecord struct {
	typ     RecordType
	payload any
}

func boardChain(t *testing.T, runID string, items []boardRecord) []Record {
	t.Helper()
	at := time.Unix(1, 0).UTC()
	prev := testManifestDigest
	records := make([]Record, 0, len(items))
	for i, item := range items {
		record, err := NewRecord(runID, uint64(i+1), item.typ, at, item.payload, prev)
		if err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
		records = append(records, record)
		prev = record.Hash
	}
	return records
}

func testArtifact() Artifact {
	content := sha256.Sum256([]byte("proof"))
	id := hex.EncodeToString(content[:])
	return Artifact{
		ID: id, RunID: "run-1", AttemptID: "attempt-1", SessionID: "executor",
		Name: "proof.txt", MediaType: "text/plain", Size: 5, SHA256: id,
		CreatedAt: time.Unix(1, 0).UTC(),
	}
}

func validBoardRecords(artifact Artifact) []boardRecord {
	at := time.Unix(1, 0).UTC()
	return []boardRecord{
		{RecordClaimCreated, ClaimCreatedRecord{Claim: Claim{ID: "origin", RunID: "run-1", TaskID: "task-1", Kind: ClaimOrigin, Description: "goal", CreatedAt: at}}},
		{RecordClaimCreated, ClaimCreatedRecord{Claim: Claim{ID: "goal", RunID: "run-1", TaskID: "task-1", Kind: ClaimGoal, Description: "goal", CreatedAt: at}}},
		{RecordIntentProposed, IntentProposedRecord{Intent: Intent{ID: "intent-1", RunID: "run-1", FromClaimIDs: []string{"origin"}, Description: "probe", State: IntentOpen, CreatedAt: at}}},
		{RecordIntentClaimed, IntentClaimedRecord{IntentID: "intent-1", ExecutorID: "executor", At: at}},
		{RecordAttemptStarted, AttemptStartedRecord{Attempt: IntentAttempt{ID: "attempt-1", RunID: "run-1", IntentID: "intent-1", SessionID: "executor", Phase: IntentAttemptRunning, StartedAt: at}}},
		{RecordArtifactStored, ArtifactStoredRecord{Artifact: artifact}},
		{RecordObservationCreated, ObservationCreatedRecord{Observation: Observation{ID: "obs-1", RunID: "run-1", IntentID: "intent-1", AttemptID: "attempt-1", Kind: "probe.result", Summary: "observed", Target: "target-1", Evidence: []Artifact{artifact}, CreatedAt: at}}},
		{RecordClaimCreated, ClaimCreatedRecord{Claim: Claim{ID: "claim-1", RunID: "run-1", TaskID: "task-1", Kind: ClaimFinding, Description: "found", ObservationIDs: []string{"obs-1"}, ArtifactIDs: []string{artifact.ID}, CreatedAt: at}}},
		{RecordAttemptConcluded, AttemptConcludedRecord{AttemptID: "attempt-1", Conclusion: "execute", At: at}},
		{RecordIntentConcluded, IntentConcludedRecord{IntentID: "intent-1", ResultClaimID: "claim-1", At: at}},
	}
}

func TestBoardApplyAndValidate(t *testing.T) {
	artifact := testArtifact()
	records := boardChain(t, "run-1", validBoardRecords(artifact))
	board := NewBoard("run-1")
	if err := board.ApplyAll(records); err != nil {
		t.Fatal(err)
	}
	if board.Revision != uint64(len(records)) {
		t.Fatalf("revision = %d, want %d", board.Revision, len(records))
	}
	if len(board.Claims) != 3 || len(board.Intents) != 1 || len(board.Attempts) != 1 ||
		len(board.Observations) != 1 || len(board.Artifacts) != 1 {
		t.Fatalf("board = %+v", board)
	}
	if board.Intents[0].State != IntentConcluded || board.Attempts[0].Phase != IntentAttemptConcluded {
		t.Fatalf("intent/attempt not concluded: %+v %+v", board.Intents[0], board.Attempts[0])
	}
}

func TestBoardNonBoardRecordLeavesRevision(t *testing.T) {
	board := NewBoard("run-1")
	at := time.Unix(1, 0).UTC()
	record, err := NewRecord("run-1", 1, RecordRunPhase, at, RunPhaseRecord{From: PhaseQueued, To: PhaseProvisioning, At: at}, testManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := board.Apply(record); err != nil {
		t.Fatal(err)
	}
	if board.Revision != 0 {
		t.Fatalf("revision = %d", board.Revision)
	}
}

func TestBoardRejectsInvalidSequences(t *testing.T) {
	artifact := testArtifact()
	cases := []struct {
		name   string
		mutate func([]boardRecord) []boardRecord
	}{
		{
			name: "intent missing source claim",
			mutate: func(records []boardRecord) []boardRecord {
				records[2].payload = IntentProposedRecord{Intent: Intent{ID: "intent-1", RunID: "run-1", FromClaimIDs: []string{"missing"}, Description: "probe", State: IntentOpen, CreatedAt: time.Unix(1, 0).UTC()}}
				return records
			},
		},
		{
			name: "finding without observation",
			mutate: func(records []boardRecord) []boardRecord {
				records[7].payload = ClaimCreatedRecord{Claim: Claim{ID: "claim-1", RunID: "run-1", TaskID: "task-1", Kind: ClaimFinding, Description: "found", CreatedAt: time.Unix(1, 0).UTC()}}
				return records
			},
		},
		{
			name: "conclude missing result claim",
			mutate: func(records []boardRecord) []boardRecord {
				records[9].payload = IntentConcludedRecord{IntentID: "intent-1", ResultClaimID: "missing", At: time.Unix(1, 0).UTC()}
				return records
			},
		},
		{
			name: "observation without evidence",
			mutate: func(records []boardRecord) []boardRecord {
				records[6].payload = ObservationCreatedRecord{Observation: Observation{ID: "obs-1", RunID: "run-1", IntentID: "intent-1", AttemptID: "attempt-1", Kind: "k", Summary: "s", Target: "target-1", CreatedAt: time.Unix(1, 0).UTC()}}
				return records
			},
		},
		{
			name: "attempt without intent",
			mutate: func(records []boardRecord) []boardRecord {
				records[4].payload = AttemptStartedRecord{Attempt: IntentAttempt{ID: "attempt-1", RunID: "run-1", IntentID: "missing", SessionID: "executor", Phase: IntentAttemptRunning, StartedAt: time.Unix(1, 0).UTC()}}
				return records
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			records := boardChain(t, "run-1", tc.mutate(validBoardRecords(artifact)))
			board := NewBoard("run-1")
			if err := board.ApplyAll(records); err == nil {
				t.Fatalf("invalid board accepted: %+v", board)
			}
		})
	}
}

func TestBoardRejectsDuplicateIDsAndRunMismatch(t *testing.T) {
	at := time.Unix(1, 0).UTC()
	board := NewBoard("run-1")
	claim := Claim{ID: "origin", RunID: "run-1", TaskID: "task-1", Kind: ClaimOrigin, Description: "goal", CreatedAt: at}
	records := boardChain(t, "run-1", []boardRecord{
		{RecordClaimCreated, ClaimCreatedRecord{Claim: claim}},
		{RecordClaimCreated, ClaimCreatedRecord{Claim: claim}},
	})
	if err := board.ApplyAll(records); err == nil {
		t.Fatal("duplicate claim accepted")
	}
	other, err := NewRecord("run-2", 1, RecordClaimCreated, at, ClaimCreatedRecord{Claim: Claim{ID: "origin", RunID: "run-2", TaskID: "task-1", Kind: ClaimOrigin, Description: "goal", CreatedAt: at}}, testManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewBoard("run-1").Apply(other); err == nil {
		t.Fatal("cross-run record accepted")
	}
}

func TestBoardValidateDetectsDanglingEvidence(t *testing.T) {
	at := time.Unix(1, 0).UTC()
	artifact := testArtifact()
	board := &Board{
		RunID: "run-1",
		Claims: []Claim{
			{ID: "origin", RunID: "run-1", TaskID: "task-1", Kind: ClaimOrigin, Description: "goal", CreatedAt: at},
			{ID: "finding", RunID: "run-1", TaskID: "task-1", Kind: ClaimFinding, Description: "found", ObservationIDs: []string{"obs-1"}, CreatedAt: at},
		},
		Intents:      []Intent{{ID: "intent-1", RunID: "run-1", FromClaimIDs: []string{"origin"}, Description: "probe", State: IntentOpen, CreatedAt: at}},
		Attempts:     []IntentAttempt{{ID: "attempt-1", RunID: "run-1", IntentID: "intent-1", SessionID: "executor", Phase: IntentAttemptRunning, StartedAt: at}},
		Observations: []Observation{{ID: "obs-1", RunID: "run-1", IntentID: "intent-1", AttemptID: "attempt-1", Kind: "k", Summary: "s", Target: "target-1", Evidence: []Artifact{artifact}, CreatedAt: at}},
	}
	if err := board.Validate(); err == nil {
		t.Fatal("dangling artifact accepted")
	}
	board.Artifacts = []Artifact{artifact}
	board.Claims[1].ArtifactIDs = []string{artifact.ID}
	if err := board.Validate(); err != nil {
		t.Fatalf("valid board rejected: %v", err)
	}
}

func TestBoardResumeNormalization(t *testing.T) {
	at := time.Unix(1, 0).UTC()
	records := boardChain(t, "run-1", []boardRecord{
		{RecordClaimCreated, ClaimCreatedRecord{Claim: Claim{ID: "origin", RunID: "run-1", TaskID: "task-1", Kind: ClaimOrigin, Description: "goal", CreatedAt: at}}},
		{RecordIntentProposed, IntentProposedRecord{Intent: Intent{ID: "intent-1", RunID: "run-1", FromClaimIDs: []string{"origin"}, Description: "probe", State: IntentOpen, CreatedAt: at}}},
		{RecordIntentClaimed, IntentClaimedRecord{IntentID: "intent-1", ExecutorID: "executor", At: at}},
		{RecordAttemptStarted, AttemptStartedRecord{Attempt: IntentAttempt{ID: "attempt-1", RunID: "run-1", IntentID: "intent-1", SessionID: "executor", Phase: IntentAttemptRunning, StartedAt: at}}},
		{RecordRunResumed, RunResumedRecord{HeadSeq: 4, Normalizations: ResumeNormalizations}},
	})
	board := NewBoard("run-1")
	if err := board.ApplyAll(records); err != nil {
		t.Fatal(err)
	}
	if board.Revision != 5 {
		t.Fatalf("revision = %d, want 5", board.Revision)
	}
	if board.Intents[0].State != IntentOpen || board.Intents[0].ExecutorID != "" || board.Intents[0].ClaimedAt != nil {
		t.Fatalf("intent not reopened: %+v", board.Intents[0])
	}
	attempt := board.Attempts[0]
	if attempt.Phase != IntentAttemptInterrupted || attempt.FinishedAt.IsZero() || attempt.Error == "" {
		t.Fatalf("attempt not interrupted: %+v", attempt)
	}
	if err := board.Validate(); err != nil {
		t.Fatalf("normalized board invalid: %v", err)
	}
}

func TestBoardRejectsUnknownResumeNormalization(t *testing.T) {
	at := time.Unix(1, 0).UTC()
	record, err := NewRecord("run-1", 1, RecordRunResumed, at, RunResumedRecord{Normalizations: []string{"unknown"}}, testManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewBoard("run-1").Apply(record); err == nil {
		t.Fatal("unknown normalization accepted")
	}
}
