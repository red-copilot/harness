package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
	"unicode/utf8"
)

// Batch is one atomic journal append: the records, the head they extend, and
// the resulting state/revision projection.
type Batch struct {
	RunID         string
	ExpectedHead  uint64
	Records       []Record
	State         *State
	BoardRevision uint64
}

// Validate checks batch continuity and the presence of records.
func (b Batch) Validate() error {
	if b.RunID == "" {
		return errors.New("batch run_id is required")
	}
	if len(b.Records) == 0 {
		return errors.New("batch requires at least one record")
	}
	for i, record := range b.Records {
		if record.RunID != b.RunID {
			return fmt.Errorf("batch record %d run_id %q does not match %q", i, record.RunID, b.RunID)
		}
		want := b.ExpectedHead + 1 + uint64(i)
		if record.Seq != want {
			return fmt.Errorf("batch record %d seq %d, want %d", i, record.Seq, want)
		}
		if i > 0 && record.PrevHash != b.Records[i-1].Hash {
			return fmt.Errorf("batch record %d prev_hash does not chain to record %d", i, i-1)
		}
	}
	return nil
}

// Finalization is the atomic terminal write: the outcome, optional verdict,
// final records and finish time.
type Finalization struct {
	RunID        string
	ExpectedHead uint64
	Outcome      Outcome
	Error        string
	Usage        Usage
	Verdict      *Verdict
	Records      []Record
	FinishedAt   time.Time
}

// Validate checks the terminal invariants before sealing.
func (f Finalization) Validate() error {
	if f.RunID == "" {
		return errors.New("finalization run_id is required")
	}
	if f.ExpectedHead == 0 {
		return errors.New("finalization expected_head is required")
	}
	if !f.Outcome.Valid() {
		return fmt.Errorf("finalization outcome %q is invalid", f.Outcome)
	}
	if f.FinishedAt.IsZero() {
		return errors.New("finalization finished_at is required")
	}
	if len(f.Records) == 0 {
		return errors.New("finalization requires final records")
	}
	for i, record := range f.Records {
		want := f.ExpectedHead + 1 + uint64(i)
		if record.Seq != want {
			return fmt.Errorf("finalization record %d seq %d, want %d", i, record.Seq, want)
		}
		if i > 0 && record.PrevHash != f.Records[i-1].Hash {
			return fmt.Errorf("finalization record %d prev_hash does not chain to record %d", i, i-1)
		}
	}
	if last := f.Records[len(f.Records)-1]; last.Type != RecordRunFinalized {
		return errors.New("finalization must end with run.finalized")
	}
	if f.Outcome.Success() {
		if f.Verdict == nil || !f.Verdict.Success {
			return errors.New("successful finalization requires a successful verdict")
		}
		if f.Verdict.Authority == VerdictAuthorityBackend {
			return errors.New("backend authority is not supported in v5")
		}
	}
	return nil
}

// SealedManifest is a redacted manifest. Its zero value is not constructible
// outside this package.
type SealedManifest struct{ m Manifest }

// Manifest returns the sealed manifest value.
func (s SealedManifest) Manifest() Manifest { return s.m }

// SealedBatch is a redacted, hash-recomputed journal batch.
type SealedBatch struct{ b Batch }

// Batch returns the sealed batch value.
func (s SealedBatch) Batch() Batch { return s.b }

// SealedArtifact is an artifact whose content has been redacted or rejected.
type SealedArtifact struct{ a ArtifactInput }

// Artifact returns the sealed artifact input.
func (s SealedArtifact) Artifact() ArtifactInput { return s.a }

// SealedLog is a redacted log record.
type SealedLog struct{ l LogRecord }

// Log returns the sealed log record.
func (s SealedLog) Log() LogRecord { return s.l }

// SealedFinalization is a redacted terminal write.
type SealedFinalization struct{ f Finalization }

// Finalization returns the sealed finalization.
func (s SealedFinalization) Finalization() Finalization { return s.f }

// SealManifest redacts and validates a manifest.
func (r *Redactor) SealManifest(m Manifest) (SealedManifest, error) {
	data, err := m.CanonicalJSON()
	if err != nil {
		return SealedManifest{}, err
	}
	redacted := r.RedactJSON(data)
	if !json.Valid(redacted) {
		return SealedManifest{}, errors.New("manifest became invalid after redaction")
	}
	var sealed Manifest
	if err := json.Unmarshal(redacted, &sealed); err != nil {
		return SealedManifest{}, fmt.Errorf("decode redacted manifest: %w", err)
	}
	if err := sealed.Validate(); err != nil {
		return SealedManifest{}, err
	}
	return SealedManifest{m: sealed}, nil
}

// SealBatch redacts every payload and recomputes the hash chain in order.
func (r *Redactor) SealBatch(b Batch) (SealedBatch, error) {
	if err := b.Validate(); err != nil {
		return SealedBatch{}, err
	}
	records, err := r.sealRecords(b.RunID, b.Records)
	if err != nil {
		return SealedBatch{}, err
	}
	b.Records = records
	if b.State != nil {
		state := *b.State
		state.Error = r.Redact(state.Error)
		b.State = &state
	}
	return SealedBatch{b: b}, nil
}

// SealFinalization redacts the error, verdict and records, then validates.
func (r *Redactor) SealFinalization(f Finalization) (SealedFinalization, error) {
	if err := f.Validate(); err != nil {
		return SealedFinalization{}, err
	}
	records, err := r.sealRecords(f.RunID, f.Records)
	if err != nil {
		return SealedFinalization{}, err
	}
	f.Records = records
	f.Error = r.Redact(f.Error)
	if f.Verdict != nil {
		verdict := *f.Verdict
		verdict.Message = r.Redact(verdict.Message)
		if len(verdict.Evidence) > 0 {
			verdict.Evidence = r.RedactJSON(verdict.Evidence)
		}
		f.Verdict = &verdict
	}
	return SealedFinalization{f: f}, nil
}

// SealLog redacts a log record's text.
func (r *Redactor) SealLog(record LogRecord) SealedLog {
	record.Text = r.Redact(record.Text)
	return SealedLog{l: record}
}

// SealArtifact redacts text content and rejects binary content containing a
// known secret. The returned input carries a fresh reader over the safe bytes.
func (r *Redactor) SealArtifact(a ArtifactInput) (SealedArtifact, error) {
	if a.Content == nil {
		return SealedArtifact{}, errors.New("artifact content is required")
	}
	data, err := io.ReadAll(a.Content)
	if err != nil {
		return SealedArtifact{}, fmt.Errorf("read artifact content: %w", err)
	}
	if utf8.Valid(data) {
		cleaned := r.Redact(string(data))
		if cleaned != string(data) {
			data = []byte(cleaned)
			a.Size = int64(len(data))
		}
	} else if r.Contains(string(data)) {
		return SealedArtifact{}, errors.New("binary artifact contains a known secret")
	}
	a.Content = bytes.NewReader(data)
	a.Size = int64(len(data))
	return SealedArtifact{a: a}, nil
}

func (r *Redactor) sealRecords(runID string, records []Record) ([]Record, error) {
	out := make([]Record, len(records))
	for i, record := range records {
		if record.RunID != runID {
			return nil, fmt.Errorf("record %d run_id %q does not match %q", i, record.RunID, runID)
		}
		original, err := ChainHash(record.PrevHash, record)
		if err != nil {
			return nil, err
		}
		if original != record.Hash {
			return nil, fmt.Errorf("record seq %d hash does not match its content", record.Seq)
		}
		payload := r.RedactJSON(record.Payload)
		if !json.Valid(payload) {
			return nil, fmt.Errorf("record seq %d payload became invalid after redaction", record.Seq)
		}
		record.Payload = payload
		if i > 0 {
			record.PrevHash = out[i-1].Hash
		}
		record.Hash, err = ChainHash(record.PrevHash, record)
		if err != nil {
			return nil, err
		}
		out[i] = record
	}
	return out, nil
}
