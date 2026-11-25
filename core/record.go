package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
)

// CanonicalJSON encodes v deterministically: struct field order, sorted map
// keys, no HTML escaping, and no trailing newline.
func CanonicalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// RecordType is the v5 journal vocabulary.
type RecordType string

const (
	RecordRunCreated         RecordType = "run.created"
	RecordRunPhase           RecordType = "run.phase"
	RecordRunState           RecordType = "run.state"
	RecordRunGoalCandidate   RecordType = "run.goal_candidate"
	RecordRunResumed         RecordType = "run.resumed"
	RecordRunInterrupted     RecordType = "run.interrupted"
	RecordRunFinalized       RecordType = "run.finalized"
	RecordTargetProvisioned  RecordType = "target.provisioned"
	RecordTargetClosed       RecordType = "target.closed"
	RecordReasonStarted      RecordType = "reason.started"
	RecordReasonCompleted    RecordType = "reason.completed"
	RecordReasonRejected     RecordType = "reason.rejected"
	RecordIntentProposed     RecordType = "intent.proposed"
	RecordIntentClaimed      RecordType = "intent.claimed"
	RecordIntentConcluded    RecordType = "intent.concluded"
	RecordIntentAbandoned    RecordType = "intent.abandoned"
	RecordAttemptStarted     RecordType = "attempt.started"
	RecordAttemptConcluded   RecordType = "attempt.concluded"
	RecordAttemptFailed      RecordType = "attempt.failed"
	RecordAttemptCancelled   RecordType = "attempt.cancelled"
	RecordAttemptTimedOut    RecordType = "attempt.timed_out"
	RecordClaimCreated       RecordType = "claim.created"
	RecordObservationCreated RecordType = "observation.created"
	RecordArtifactStored     RecordType = "artifact.stored"
	RecordContextTruncated   RecordType = "context.truncated"
	RecordAgentEvent         RecordType = "agent.event"
	RecordVerifierStarted    RecordType = "verifier.started"
	RecordVerifierCompleted  RecordType = "verifier.completed"
	RecordControlError       RecordType = "control.error"
	RecordBudgetExceeded     RecordType = "budget.exceeded"
)

// Record payloads. The record envelope carries the common fields; payloads
// carry only the semantic content of one operation.
type (
	RunCreatedRecord struct {
		TaskID         string `json:"task_id"`
		TargetID       string `json:"target_id"`
		ManifestDigest string `json:"manifest_digest"`
		ReasonerID     string `json:"reasoner_id"`
		ExecutorID     string `json:"executor_id"`
		PolicyDigest   string `json:"policy_digest"`
	}
	RunPhaseRecord struct {
		From   Phase     `json:"from"`
		To     Phase     `json:"to"`
		Reason string    `json:"reason,omitempty"`
		At     time.Time `json:"at"`
	}
	RunStateRecord struct {
		Usage           Usage `json:"usage"`
		GoalCandidate   bool  `json:"goal_candidate,omitempty"`
		NonGrowthRounds int   `json:"non_growth_rounds,omitempty"`
		ReasonFailures  int   `json:"reason_failures,omitempty"`
		Stagnated       bool  `json:"stagnated,omitempty"`
	}
	RunGoalCandidateRecord struct {
		Reason string    `json:"reason,omitempty"`
		At     time.Time `json:"at"`
	}
	RunResumedRecord struct {
		HeadSeq        uint64   `json:"head_seq"`
		Normalizations []string `json:"normalizations,omitempty"`
	}
	RunInterruptedRecord struct {
		Reason   string    `json:"reason"`
		Operator string    `json:"operator,omitempty"`
		At       time.Time `json:"at"`
	}
	RunFinalizedRecord struct {
		Outcome       Outcome   `json:"outcome"`
		VerdictSHA256 string    `json:"verdict_sha256,omitempty"`
		Error         string    `json:"error,omitempty"`
		Usage         Usage     `json:"usage"`
		At            time.Time `json:"at"`
	}
	TargetProvisionedRecord struct {
		Binding       TargetBinding `json:"binding"`
		RuntimeDigest string        `json:"runtime_digest,omitempty"`
	}
	TargetClosedRecord struct {
		Error string `json:"error,omitempty"`
	}
	ReasonStartedRecord struct {
		BoardRevision   uint64 `json:"board_revision"`
		CanCreateIntent bool   `json:"can_create_intent"`
	}
	ReasonCompletedRecord struct {
		IntentIDs     []string `json:"intent_ids,omitempty"`
		GoalCandidate bool     `json:"goal_candidate,omitempty"`
	}
	ReasonRejectedRecord struct {
		Error string    `json:"error"`
		At    time.Time `json:"at"`
	}
	IntentProposedRecord struct {
		Intent Intent `json:"intent"`
	}
	IntentClaimedRecord struct {
		IntentID   string    `json:"intent_id"`
		ExecutorID string    `json:"executor_id"`
		At         time.Time `json:"at"`
	}
	IntentConcludedRecord struct {
		IntentID      string    `json:"intent_id"`
		ResultClaimID string    `json:"result_claim_id"`
		At            time.Time `json:"at"`
	}
	IntentAbandonedRecord struct {
		IntentID string    `json:"intent_id"`
		Reason   string    `json:"reason"`
		At       time.Time `json:"at"`
	}
	AttemptStartedRecord struct {
		Attempt IntentAttempt `json:"attempt"`
	}
	AttemptConcludedRecord struct {
		AttemptID  string    `json:"attempt_id"`
		Conclusion string    `json:"conclusion,omitempty"`
		At         time.Time `json:"at"`
	}
	AttemptFailedRecord struct {
		AttemptID string    `json:"attempt_id"`
		Error     string    `json:"error,omitempty"`
		At        time.Time `json:"at"`
	}
	AttemptCancelledRecord struct {
		AttemptID string    `json:"attempt_id"`
		At        time.Time `json:"at"`
	}
	AttemptTimedOutRecord struct {
		AttemptID string    `json:"attempt_id"`
		At        time.Time `json:"at"`
	}
	ClaimCreatedRecord struct {
		Claim Claim `json:"claim"`
	}
	ObservationCreatedRecord struct {
		Observation Observation `json:"observation"`
	}
	ArtifactStoredRecord struct {
		Artifact Artifact `json:"artifact"`
	}
	ContextTruncatedRecord struct {
		Context    string `json:"context"`
		Candidates int    `json:"candidates"`
		Kept       int    `json:"kept"`
		Dropped    int    `json:"dropped"`
	}
	AgentEventRecord struct {
		NativeType string          `json:"native_type"`
		Data       json.RawMessage `json:"data,omitempty"`
	}
	VerifierStartedRecord struct {
		EvidenceDigest string `json:"evidence_digest,omitempty"`
	}
	VerifierCompletedRecord struct {
		VerdictSHA256 string `json:"verdict_sha256,omitempty"`
		DurationMs    int64  `json:"duration_ms,omitempty"`
	}
	ControlErrorRecord struct {
		Error string    `json:"error"`
		At    time.Time `json:"at"`
	}
	BudgetExceededRecord struct {
		Dimension string    `json:"dimension"`
		Limit     int64     `json:"limit"`
		At        time.Time `json:"at"`
	}
)

// Resume normalizations. A resumed Run has no executor attempt in flight, so
// running attempts become interrupted and claimed intents return to the open
// column. The run.resumed record makes the normalization replayable.
const (
	NormalizeAttemptsInterrupted = "attempts.interrupted"
	NormalizeIntentsReopened     = "intents.reopened"
)

// ResumeNormalizations is the deterministic normalization set applied when a
// Run is resumed.
var ResumeNormalizations = []string{NormalizeAttemptsInterrupted, NormalizeIntentsReopened}

// Record is one canonical journal entry. Hash covers the envelope fields and
// the exact payload bytes, chained through PrevHash.
type Record struct {
	RunID         string          `json:"run_id"`
	Seq           uint64          `json:"seq"`
	Type          RecordType      `json:"type"`
	SchemaVersion int             `json:"schema_version"`
	At            time.Time       `json:"at"`
	Payload       json.RawMessage `json:"payload"`
	PrevHash      string          `json:"prev_hash"`
	Hash          string          `json:"hash"`
}

type recordContract struct {
	version int
	payload any
}

var recordContracts = map[RecordType]recordContract{
	RecordRunCreated:         {1, RunCreatedRecord{}},
	RecordRunPhase:           {1, RunPhaseRecord{}},
	RecordRunState:           {1, RunStateRecord{}},
	RecordRunGoalCandidate:   {1, RunGoalCandidateRecord{}},
	RecordRunResumed:         {1, RunResumedRecord{}},
	RecordRunInterrupted:     {1, RunInterruptedRecord{}},
	RecordRunFinalized:       {1, RunFinalizedRecord{}},
	RecordTargetProvisioned:  {1, TargetProvisionedRecord{}},
	RecordTargetClosed:       {1, TargetClosedRecord{}},
	RecordReasonStarted:      {1, ReasonStartedRecord{}},
	RecordReasonCompleted:    {1, ReasonCompletedRecord{}},
	RecordReasonRejected:     {1, ReasonRejectedRecord{}},
	RecordIntentProposed:     {1, IntentProposedRecord{}},
	RecordIntentClaimed:      {1, IntentClaimedRecord{}},
	RecordIntentConcluded:    {1, IntentConcludedRecord{}},
	RecordIntentAbandoned:    {1, IntentAbandonedRecord{}},
	RecordAttemptStarted:     {1, AttemptStartedRecord{}},
	RecordAttemptConcluded:   {1, AttemptConcludedRecord{}},
	RecordAttemptFailed:      {1, AttemptFailedRecord{}},
	RecordAttemptCancelled:   {1, AttemptCancelledRecord{}},
	RecordAttemptTimedOut:    {1, AttemptTimedOutRecord{}},
	RecordClaimCreated:       {1, ClaimCreatedRecord{}},
	RecordObservationCreated: {1, ObservationCreatedRecord{}},
	RecordArtifactStored:     {1, ArtifactStoredRecord{}},
	RecordContextTruncated:   {1, ContextTruncatedRecord{}},
	RecordAgentEvent:         {1, AgentEventRecord{}},
	RecordVerifierStarted:    {1, VerifierStartedRecord{}},
	RecordVerifierCompleted:  {1, VerifierCompletedRecord{}},
	RecordControlError:       {1, ControlErrorRecord{}},
	RecordBudgetExceeded:     {1, BudgetExceededRecord{}},
}

// RecordTypes returns the registered vocabulary in deterministic order.
func RecordTypes() []RecordType {
	out := make([]RecordType, 0, len(recordContracts))
	for typ := range recordContracts {
		out = append(out, typ)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// RecordSchemaVersion returns the current version for a record type.
func RecordSchemaVersion(typ RecordType) (int, bool) {
	contract, ok := recordContracts[typ]
	if !ok {
		return 0, false
	}
	return contract.version, true
}

type recordHashInput struct {
	RunID         string          `json:"run_id"`
	Seq           uint64          `json:"seq"`
	Type          RecordType      `json:"type"`
	SchemaVersion int             `json:"schema_version"`
	AtUnixNano    int64           `json:"at_unix_nano"`
	Payload       json.RawMessage `json:"payload"`
}

// ChainHash computes the record hash from the previous hash and the record.
func ChainHash(prev string, record Record) (string, error) {
	if len(prev) != 64 {
		return "", errors.New("previous hash must be 64 hex characters")
	}
	input, err := CanonicalJSON(recordHashInput{
		RunID: record.RunID, Seq: record.Seq, Type: record.Type,
		SchemaVersion: record.SchemaVersion, AtUnixNano: record.At.UTC().UnixNano(),
		Payload: record.Payload,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte(prev+"\n"), input...))
	return hex.EncodeToString(sum[:]), nil
}

// NewRecord validates the payload type, canonicalizes it, and seals the hash.
func NewRecord(runID string, seq uint64, typ RecordType, at time.Time, payload any, prevHash string) (Record, error) {
	if strings.TrimSpace(runID) == "" {
		return Record{}, errors.New("record run_id is required")
	}
	if seq == 0 {
		return Record{}, errors.New("record seq must be at least 1")
	}
	if at.IsZero() {
		return Record{}, errors.New("record time is required")
	}
	contract, ok := recordContracts[typ]
	if !ok {
		return Record{}, fmt.Errorf("unknown record type %q", typ)
	}
	if reflect.TypeOf(payload) != reflect.TypeOf(contract.payload) {
		return Record{}, fmt.Errorf("record %q payload type %T does not match %T", typ, payload, contract.payload)
	}
	data, err := CanonicalJSON(payload)
	if err != nil {
		return Record{}, fmt.Errorf("marshal record %q: %w", typ, err)
	}
	record := Record{
		RunID: runID, Seq: seq, Type: typ, SchemaVersion: contract.version,
		At: at.UTC(), Payload: data, PrevHash: prevHash,
	}
	hash, err := ChainHash(prevHash, record)
	if err != nil {
		return Record{}, err
	}
	record.Hash = hash
	return record, nil
}

// DecodeRecord returns the typed payload for a record.
func DecodeRecord(record Record) (any, error) {
	contract, ok := recordContracts[record.Type]
	if !ok {
		return nil, fmt.Errorf("unknown record type %q", record.Type)
	}
	if record.SchemaVersion != contract.version {
		return nil, fmt.Errorf("record %q schema version %d is not supported (want %d)", record.Type, record.SchemaVersion, contract.version)
	}
	value := reflect.New(reflect.TypeOf(contract.payload))
	if err := json.Unmarshal(record.Payload, value.Interface()); err != nil {
		return nil, fmt.Errorf("decode record %q: %w", record.Type, err)
	}
	return value.Elem().Interface(), nil
}

// VerifyRecordChain checks sequence continuity, previous-hash linkage, payload
// decoding and every recomputed hash.
func VerifyRecordChain(manifestDigest string, records []Record) error {
	if len(manifestDigest) != 64 {
		return errors.New("manifest digest must be 64 hex characters")
	}
	if len(records) > 0 {
		runID := records[0].RunID
		if runID == "" {
			return errors.New("records require a run_id")
		}
		for _, record := range records {
			if record.RunID != runID {
				return fmt.Errorf("record run_id %q does not match %q", record.RunID, runID)
			}
		}
	}
	prev := manifestDigest
	for i, record := range records {
		if record.Seq != uint64(i+1) {
			return fmt.Errorf("record %d has seq %d, want %d", i, record.Seq, i+1)
		}
		if record.PrevHash != prev {
			return fmt.Errorf("record seq %d prev_hash %s does not match %s", record.Seq, record.PrevHash, prev)
		}
		if _, err := DecodeRecord(record); err != nil {
			return err
		}
		hash, err := ChainHash(prev, record)
		if err != nil {
			return err
		}
		if hash != record.Hash {
			return fmt.Errorf("record seq %d hash %s does not match recomputed %s", record.Seq, record.Hash, hash)
		}
		prev = record.Hash
	}
	return nil
}
