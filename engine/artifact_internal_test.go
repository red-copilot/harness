package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadEvidenceFileEnforcesActualByteLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evidence.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 128)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readEvidenceFile(path, 16); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v, want byte limit rejection", err)
	}
}

func TestReadEvidenceFileRejectsSymlinkAtOpenBoundary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	link := filepath.Join(dir, "link.txt")
	if err := os.WriteFile(target, []byte("evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readEvidenceFile(link, 1024); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("err = %v, want symlink rejection", err)
	}
}
