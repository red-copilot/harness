package harness_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/red-copilot/harness"
)

func TestValidateEvidencePathRejectsAbsoluteTraversalAndSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.ValidateEvidencePath(root, filepath.Join(root, "ok.txt")); err == nil {
		t.Fatal("absolute path accepted")
	}
	if _, err := harness.ValidateEvidencePath(root, "../secret"); err == nil {
		t.Fatal("traversal accepted")
	}
	if _, err := harness.ValidateEvidencePath(root, "."); err == nil {
		t.Fatal("directory accepted")
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.ValidateEvidencePath(root, "link"); err == nil {
		t.Fatal("symlink escape accepted")
	}
}

func TestArchiveEvidenceRejectsFileLimitBeforePut(t *testing.T) {
	root := t.TempDir()
	observations := []harness.ObservationProposal{{LocalID: "obs-1", Kind: "adapter.kind", Summary: "many", Target: "target-1"}}
	for i := 0; i < 17; i++ {
		name := "file-" + string(rune('a'+i))
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		observations[0].EvidenceFiles = append(observations[0].EvidenceFiles, name)
	}
	store := &fakeArtifactStore{}
	_, err := harness.ArchiveAttemptEvidence(context.Background(), store, *harness.NewRedactor(), harness.EvidenceArchiveRequest{RunID: "run", IntentID: "branch", AttemptID: "attempt", SessionID: "session", WorkspaceDir: root, Target: harness.TargetBinding{TargetID: "target-1", Isolation: harness.IsolationCallerManaged}, Observations: observations, MaxFiles: 16})
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v", err)
	}
	if store.puts != 0 {
		t.Fatalf("artifact puts = %d, want 0", store.puts)
	}
}

func TestArchiveEvidenceRejectsOversizeBeforePut(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "small"), []byte("small"), 0o600)
	_ = os.WriteFile(filepath.Join(root, "large"), bytes.Repeat([]byte("x"), 32), 0o600)
	observations := []harness.ObservationProposal{{LocalID: "one", Kind: "k", Summary: "one", Target: "target-1", EvidenceFiles: []string{"small"}}, {LocalID: "two", Kind: "k", Summary: "two", Target: "target-1", EvidenceFiles: []string{"large"}}}
	store := &fakeArtifactStore{}
	_, err := harness.ArchiveAttemptEvidence(context.Background(), store, *harness.NewRedactor(), harness.EvidenceArchiveRequest{RunID: "run", IntentID: "branch", AttemptID: "attempt", SessionID: "session", WorkspaceDir: root, Target: harness.TargetBinding{TargetID: "target-1", Isolation: harness.IsolationCallerManaged}, Observations: observations, MaxFiles: 16, MaxFileBytes: 16, MaxTotalBytes: 64})
	if err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("err = %v", err)
	}
	if store.puts != 0 {
		t.Fatalf("artifact puts = %d, want 0", store.puts)
	}
}

func TestArchiveEvidenceUsesContentHash(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "a.txt"), []byte("same"), 0o600)
	_ = os.WriteFile(filepath.Join(root, "b.txt"), []byte("same"), 0o600)
	store := &fakeArtifactStore{}
	observations, err := harness.ArchiveAttemptEvidence(context.Background(), store, *harness.NewRedactor(), harness.EvidenceArchiveRequest{RunID: "run", IntentID: "branch", AttemptID: "attempt", SessionID: "session", WorkspaceDir: root, Target: harness.TargetBinding{TargetID: "target-1", Isolation: harness.IsolationCallerManaged}, Observations: []harness.ObservationProposal{{LocalID: "obs", Kind: "k", Summary: "s", Target: "target-1", EvidenceFiles: []string{"a.txt", "b.txt"}}}, MaxFiles: 16, MaxFileBytes: 1024, MaxTotalBytes: 2048})
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 1 || len(observations[0].Evidence) != 2 {
		t.Fatalf("observations = %+v", observations)
	}
	if observations[0].Evidence[0].SHA256 != sha256Hex([]byte("same")) || observations[0].Evidence[0].ID != observations[0].Evidence[1].ID {
		t.Fatalf("hashes = %+v", observations[0].Evidence)
	}
}

func TestArchiveEvidenceRequiresObservationFile(t *testing.T) {
	store := &fakeArtifactStore{}
	_, err := harness.ArchiveAttemptEvidence(context.Background(), store, *harness.NewRedactor(), harness.EvidenceArchiveRequest{RunID: "run", IntentID: "branch", AttemptID: "attempt", SessionID: "session", WorkspaceDir: t.TempDir(), Target: harness.TargetBinding{TargetID: "target-1", Isolation: harness.IsolationCallerManaged}, Observations: []harness.ObservationProposal{{LocalID: "obs", Kind: "k", Summary: "s", Target: "target-1"}}})
	if err == nil || !strings.Contains(err.Error(), "evidence") {
		t.Fatalf("err = %v", err)
	}
}

func TestArchiveEvidenceRequiresArtifactWrite(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "proof"), []byte("proof"), 0o600)
	store := &fakeArtifactStore{fail: errors.New("disk full")}
	_, err := harness.ArchiveAttemptEvidence(context.Background(), store, *harness.NewRedactor(), harness.EvidenceArchiveRequest{RunID: "run", IntentID: "branch", AttemptID: "attempt", SessionID: "session", WorkspaceDir: root, Target: harness.TargetBinding{TargetID: "target-1", Isolation: harness.IsolationCallerManaged}, Observations: []harness.ObservationProposal{{LocalID: "obs", Kind: "k", Summary: "s", Target: "target-1", EvidenceFiles: []string{"proof"}}}})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v", err)
	}
}

func TestTextArtifactIsRedactedBeforeHash(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "proof.txt"), []byte("token=supersecret"), 0o600)
	store := &fakeArtifactStore{}
	obs, err := harness.ArchiveAttemptEvidence(context.Background(), store, *harness.NewRedactor("supersecret"), harness.EvidenceArchiveRequest{RunID: "run", IntentID: "branch", AttemptID: "attempt", SessionID: "session", WorkspaceDir: root, Target: harness.TargetBinding{TargetID: "target-1", Isolation: harness.IsolationCallerManaged}, Observations: []harness.ObservationProposal{{LocalID: "obs", Kind: "k", Summary: "s", Target: "target-1", EvidenceFiles: []string{"proof.txt"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !obs[0].Evidence[0].Redacted {
		t.Fatal("artifact not marked redacted")
	}
	if obs[0].Evidence[0].SHA256 != sha256Hex([]byte("token=[REDACTED]")) {
		t.Fatalf("hash = %s", obs[0].Evidence[0].SHA256)
	}
}

func TestBinaryArtifactWithKnownSecretIsRejected(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "proof.bin"), []byte{0, 1, 2, 's', 'u', 'p', 'e', 'r', 's', 'e', 'c', 'r', 'e', 't', 0xff}, 0o600)
	store := &fakeArtifactStore{}
	_, err := harness.ArchiveAttemptEvidence(context.Background(), store, *harness.NewRedactor("supersecret"), harness.EvidenceArchiveRequest{RunID: "run", IntentID: "branch", AttemptID: "attempt", SessionID: "session", WorkspaceDir: root, Target: harness.TargetBinding{TargetID: "target-1", Isolation: harness.IsolationCallerManaged}, Observations: []harness.ObservationProposal{{LocalID: "obs", Kind: "k", Summary: "s", Target: "target-1", EvidenceFiles: []string{"proof.bin"}}}})
	if err == nil || !strings.Contains(err.Error(), "secret") {
		t.Fatalf("err = %v", err)
	}
	if store.puts != 0 {
		t.Fatalf("puts = %d", store.puts)
	}
}
