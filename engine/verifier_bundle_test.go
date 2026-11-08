package engine

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type bundleArtifactStore struct {
	Store
	artifacts []Artifact
	data      map[string][]byte
}

func (s *bundleArtifactStore) ListArtifacts(context.Context, string) ([]Artifact, error) {
	return append([]Artifact(nil), s.artifacts...), nil
}

func (s *bundleArtifactStore) OpenArtifact(_ context.Context, id string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.data[id])), nil
}

func TestBuildEvidenceBundleAllowsSameContentAcrossAttempts(t *testing.T) {
	content := []byte("proof")
	id := ArtifactContentID(content)
	store := &bundleArtifactStore{
		artifacts: []Artifact{
			{ID: id, RunID: "run", AttemptID: "attempt-1", Name: "proof.txt", MediaType: "text/plain", Size: int64(len(content)), SHA256: id},
			{ID: id, RunID: "run", AttemptID: "attempt-2", Name: "proof.txt", MediaType: "text/plain", Size: int64(len(content)), SHA256: id},
		},
		data: map[string][]byte{id: content},
	}
	dir, err := buildEvidenceBundle(context.Background(), store, "run", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, attempt := range []string{"attempt-1", "attempt-2"} {
		path := filepath.Join(dir, id[:2], id, attempt, "proof.txt")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !bytes.Equal(data, content) {
			t.Fatalf("%s = %q, want %q", path, data, content)
		}
	}
}
