package core

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func sealedText(t *testing.T, reader io.Reader) string {
	t.Helper()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSealManifestRedactsSecret(t *testing.T) {
	secret := "supersecret"
	m := testManifest()
	m.Task.Objective = "use " + secret
	sealed, err := NewRedactor(secret).SealManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed.Manifest().Task.Objective, secret) {
		t.Fatalf("secret leaked: %q", sealed.Manifest().Task.Objective)
	}
	if !strings.Contains(sealed.Manifest().Task.Objective, RedactedValue) {
		t.Fatalf("secret not replaced: %q", sealed.Manifest().Task.Objective)
	}
	if err := sealed.Manifest().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSealBatchRedactsAndRechains(t *testing.T) {
	secret := "supersecret"
	at := time.Unix(1, 0).UTC()
	first, err := NewRecord("run-1", 1, RecordClaimCreated, at, ClaimCreatedRecord{Claim: Claim{
		ID: "origin", RunID: "run-1", TaskID: "task-1", Kind: ClaimOrigin,
		Description: "use " + secret, CreatedAt: at,
	}}, testManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewRecord("run-1", 2, RecordClaimCreated, at, ClaimCreatedRecord{Claim: Claim{
		ID: "goal", RunID: "run-1", TaskID: "task-1", Kind: ClaimGoal,
		Description: "use " + secret, CreatedAt: at,
	}}, first.Hash)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := NewRedactor(secret).SealBatch(Batch{RunID: "run-1", Records: []Record{first, second}})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRecordChain(testManifestDigest, sealed.Batch().Records); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(sealed.Batch().Records)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("secret leaked: %s", raw)
	}
}

func TestSealFinalizationRedactsAndValidates(t *testing.T) {
	secret := "supersecret"
	at := time.Unix(1, 0).UTC()
	final, err := NewRecord("run-1", 3, RecordRunFinalized, at, RunFinalizedRecord{
		Outcome: OutcomeFailed, Error: "boom " + secret, At: at,
	}, testManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	finalization := Finalization{
		RunID: "run-1", ExpectedHead: 2, Outcome: OutcomeFailed,
		Error: "boom " + secret, FinishedAt: at,
		Verdict: &Verdict{Success: false, Message: "failed " + secret},
		Records: []Record{final},
	}
	sealed, err := NewRedactor(secret).SealFinalization(finalization)
	if err != nil {
		t.Fatal(err)
	}
	got := sealed.Finalization()
	if strings.Contains(got.Error, secret) || strings.Contains(got.Verdict.Message, secret) {
		t.Fatalf("secret leaked: %+v", got)
	}
	raw, err := json.Marshal(got.Records)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("secret leaked in records: %s", raw)
	}
}

func TestSealFinalizationAcceptsSuccessfulVerdict(t *testing.T) {
	at := time.Unix(1, 0).UTC()
	final, err := NewRecord("run-1", 3, RecordRunFinalized, at, RunFinalizedRecord{Outcome: OutcomeSucceeded, At: at}, testManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	finalization := Finalization{
		RunID: "run-1", ExpectedHead: 2, Outcome: OutcomeSucceeded, FinishedAt: at,
		Verdict: &Verdict{Authority: VerdictAuthorityVerifier, Success: true},
		Records: []Record{final},
	}
	if _, err := NewRedactor().SealFinalization(finalization); err != nil {
		t.Fatal(err)
	}
}

func TestSealLogRedactsText(t *testing.T) {
	secret := "supersecret"
	sealed := NewRedactor(secret).SealLog(LogRecord{RunID: "run-1", Text: "token=" + secret})
	if strings.Contains(sealed.Log().Text, secret) {
		t.Fatalf("secret leaked: %q", sealed.Log().Text)
	}
}

func TestSealArtifactRedactsTextAndRejectsBinarySecrets(t *testing.T) {
	secret := "supersecret"
	sealed, err := NewRedactor(secret).SealArtifact(ArtifactInput{
		RunID: "run-1", AttemptID: "attempt-1", Name: "proof.txt",
		Content: strings.NewReader("token=" + secret),
	})
	if err != nil {
		t.Fatal(err)
	}
	input := sealed.Artifact()
	if strings.Contains(sealedText(t, input.Content), secret) {
		t.Fatal("secret leaked in text artifact")
	}
	if input.Size != int64(len("token="+RedactedValue)) {
		t.Fatalf("size = %d", input.Size)
	}
	if _, err := NewRedactor(secret).SealArtifact(ArtifactInput{
		RunID: "run-1", AttemptID: "attempt-1", Name: "proof.bin",
		Content: bytes.NewReader(append([]byte{0xff, 0xfe}, []byte(secret)...)),
	}); err == nil {
		t.Fatal("binary artifact with a known secret accepted")
	}
}
