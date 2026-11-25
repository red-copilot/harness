package runtime

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/red-copilot/harness/engine"
)

func TestOpenEvidenceEnforcesByteLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "evidence.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 128)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (OSWorkspace{}).OpenEvidence(context.Background(), dir, "evidence.txt", 16); err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("err = %v, want size limit rejection", err)
	}
}

func TestOpenEvidenceRejectsSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := (OSWorkspace{}).OpenEvidence(context.Background(), dir, "link.txt", 1024); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("err = %v, want symlink escape rejection", err)
	}
}

func TestOpenEvidenceRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	if _, err := (OSWorkspace{}).OpenEvidence(context.Background(), dir, "../secret", 1024); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("err = %v, want traversal rejection", err)
	}
}

func TestPrepareRunCreatesDirectoriesAndMaterializesSkills(t *testing.T) {
	root := t.TempDir()
	materialized := ""
	dirs, err := (OSWorkspace{}).PrepareRun(context.Background(), root, "run-1", func(skills string) error {
		materialized = skills
		return os.WriteFile(filepath.Join(skills, "skill.md"), []byte("skill"), 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	if dirs.Root() != filepath.Join(root, "run-1") {
		t.Fatalf("root = %q", dirs.Root())
	}
	if materialized != dirs.SkillsDir() {
		t.Fatalf("materialized = %q, skills = %q", materialized, dirs.SkillsDir())
	}
	session, err := dirs.PrepareSession(context.Background(), "planner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(session, "..", "task")); err != nil {
		t.Fatalf("task dir missing: %v", err)
	}
}

type fakeArtifactReader struct {
	artifacts []engine.Artifact
	data      map[string][]byte
}

func (f fakeArtifactReader) ListArtifacts(context.Context, string) ([]engine.Artifact, error) {
	return append([]engine.Artifact(nil), f.artifacts...), nil
}

func (f fakeArtifactReader) OpenArtifact(_ context.Context, id string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(f.data[id])), nil
}

func TestMaterializeEvidenceAllowsSameContentAcrossAttempts(t *testing.T) {
	content := []byte("proof")
	id := engine.ArtifactContentID(content)
	reader := fakeArtifactReader{
		artifacts: []engine.Artifact{
			{ID: id, RunID: "run", AttemptID: "attempt-1", Name: "proof.txt", MediaType: "text/plain", Size: int64(len(content)), SHA256: id},
			{ID: id, RunID: "run", AttemptID: "attempt-2", Name: "proof.txt", MediaType: "text/plain", Size: int64(len(content)), SHA256: id},
		},
		data: map[string][]byte{id: content},
	}
	dir, cleanup, err := (OSWorkspace{}).MaterializeEvidence(context.Background(), t.TempDir(), reader.artifacts, reader.OpenArtifact)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cleanup() }()
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
