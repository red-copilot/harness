package harness_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/red-copilot/harness"
)

func TestTargetFingerprintUsesStableSpecIdentity(t *testing.T) {
	first := harness.TargetSpec{ID: " target-1 ", Addresses: []string{"10.0.0.1:80"}, Isolation: harness.IsolationCallerManaged}
	second := harness.TargetSpec{ID: "target-1", Addresses: []string{"10.0.0.9:443"}, Isolation: harness.IsolationCallerManaged}
	if got, want := harness.TargetFingerprint(first), harness.TargetFingerprint(second); got != want {
		t.Fatalf("fingerprints differ: %q != %q", got, want)
	}
}

func TestTargetBindingMustPreserveAuthorizedIdentity(t *testing.T) {
	spec := harness.TargetSpec{ID: "target-1", Isolation: harness.IsolationCallerManaged}
	binding := harness.TargetBinding{TargetID: "target-2", Isolation: harness.IsolationCallerManaged}
	if err := harness.ValidateTargetBinding(spec, binding); err == nil {
		t.Fatal("binding changed target identity")
	}
}

func TestAuthorizeTargetUsesEffectiveBinding(t *testing.T) {
	binding := harness.TargetBinding{
		TargetID:  "target-1",
		Addresses: []string{"10.0.0.1:80"},
		Isolation: harness.IsolationCallerManaged,
	}
	for _, allowed := range []string{"target-1", "10.0.0.1:80", "http://10.0.0.1/admin"} {
		if !harness.AuthorizeTarget(allowed, binding) {
			t.Fatalf("expected %q to be authorized", allowed)
		}
	}
	for _, denied := range []string{"", "target-2", "10.0.0.2:80", "https://example.com"} {
		if harness.AuthorizeTarget(denied, binding) {
			t.Fatalf("expected %q to be rejected", denied)
		}
	}
}

func TestArchiveEvidenceRejectsUnauthorizedObservationTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "proof.txt"), []byte("proof"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &fakeArtifactStore{}
	_, err := harness.ArchiveAttemptEvidence(context.Background(), store, *harness.NewRedactor(), harness.EvidenceArchiveRequest{
		RunID:        "run",
		IntentID:     "branch",
		AttemptID:    "attempt",
		SessionID:    "session",
		WorkspaceDir: root,
		Target: harness.TargetBinding{
			TargetID:  "target-1",
			Addresses: []string{"10.0.0.1:80"},
			Isolation: harness.IsolationCallerManaged,
		},
		Observations: []harness.ObservationProposal{{
			LocalID:       "obs",
			Kind:          "adapter.kind",
			Summary:       "summary",
			Target:        "10.0.0.2:80",
			EvidenceFiles: []string{"proof.txt"},
		}},
		MaxFiles:      16,
		MaxFileBytes:  1024,
		MaxTotalBytes: 2048,
	})
	if err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("err = %v, want unauthorized target", err)
	}
	if store.puts != 0 {
		t.Fatalf("artifact puts = %d, want 0", store.puts)
	}
}
