package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/red-copilot/harness/engine"
)

// OSWorkspace is the production Workspace implementation. It is the only
// package that performs kernel-adjacent filesystem I/O.
type OSWorkspace struct{}

type osRunDirs struct {
	root      string
	taskDir   string
	skillsDir string
}

func (d osRunDirs) Root() string                       { return d.root }
func (d osRunDirs) TaskDir() string                    { return d.taskDir }
func (d osRunDirs) SkillsDir() string                  { return d.skillsDir }
func (d osRunDirs) SessionDir(sessionID string) string { return filepath.Join(d.root, sessionID) }

func (d osRunDirs) PrepareSession(_ context.Context, sessionID string) (string, error) {
	if strings.TrimSpace(sessionID) == "" {
		return "", errors.New("session id is required")
	}
	dir := d.SessionDir(sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create session workspace: %w", err)
	}
	return dir, nil
}

// PrepareRun creates the run/task/skills directories and materializes skills.
func (OSWorkspace) PrepareRun(_ context.Context, root, runID string, materializeSkills func(string) error) (engine.RunDirs, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("workspace root is required")
	}
	if strings.TrimSpace(runID) == "" {
		return nil, errors.New("run id is required")
	}
	runRoot := filepath.Join(root, runID)
	taskDir := filepath.Join(runRoot, "task")
	skillsDir := filepath.Join(runRoot, "skills")
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		return nil, fmt.Errorf("create task directory: %w", err)
	}
	if err := os.MkdirAll(skillsDir, 0o700); err != nil {
		return nil, fmt.Errorf("create skills directory: %w", err)
	}
	if materializeSkills != nil {
		if err := materializeSkills(skillsDir); err != nil {
			return nil, fmt.Errorf("materialize skills: %w", err)
		}
	}
	return osRunDirs{root: runRoot, taskDir: taskDir, skillsDir: skillsDir}, nil
}

// ClearReport removes the stale report before a turn starts.
func (OSWorkspace) ClearReport(_ context.Context, workspaceDir string) error {
	if strings.TrimSpace(workspaceDir) == "" {
		return errors.New("report workspace is required")
	}
	path := filepath.Join(workspaceDir, engine.ReportFileName)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear report: %w", err)
	}
	return nil
}

// ReadReport reads the bounded, regular report file from the workspace.
func (OSWorkspace) ReadReport(_ context.Context, workspaceDir string, maxBytes int64) ([]byte, error) {
	if strings.TrimSpace(workspaceDir) == "" {
		return nil, errors.New("report workspace is required")
	}
	root, err := filepath.EvalSymlinks(workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("resolve report workspace: %w", err)
	}
	path := filepath.Join(root, engine.ReportFileName)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("read report: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("report must be a regular file")
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		return nil, fmt.Errorf("report exceeds %d bytes", maxBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read report: %w", err)
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("report exceeds %d bytes", maxBytes)
	}
	return data, nil
}

// ValidateEvidencePath resolves a workspace-relative regular file and rejects
// traversal, symlink escape and directories.
func ValidateEvidencePath(root, candidate string) (string, error) {
	_, evalPath, _, _, err := resolveEvidence(root, candidate)
	if err != nil {
		return "", err
	}
	return evalPath, nil
}

// OpenEvidence opens a confined workspace-relative file for reading.
func (OSWorkspace) OpenEvidence(_ context.Context, workspaceDir, relPath string, maxBytes int64) (engine.EvidenceFile, error) {
	_, evalPath, rel, info, err := resolveEvidence(workspaceDir, relPath)
	if err != nil {
		return engine.EvidenceFile{}, err
	}
	file, err := os.Open(evalPath)
	if err != nil {
		return engine.EvidenceFile{}, fmt.Errorf("open evidence: %w", err)
	}
	after, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return engine.EvidenceFile{}, fmt.Errorf("stat open evidence: %w", err)
	}
	current, err := os.Lstat(evalPath)
	if err != nil || !os.SameFile(info, after) || !os.SameFile(after, current) {
		_ = file.Close()
		return engine.EvidenceFile{}, fmt.Errorf("evidence path changed while opening: %q", relPath)
	}
	if !after.Mode().IsRegular() {
		_ = file.Close()
		return engine.EvidenceFile{}, fmt.Errorf("evidence path is not a regular file: %q", relPath)
	}
	if maxBytes > 0 && after.Size() > maxBytes {
		_ = file.Close()
		return engine.EvidenceFile{}, fmt.Errorf("evidence file %q exceeds size limit", relPath)
	}
	var reader io.Reader = file
	if maxBytes > 0 {
		reader = io.LimitReader(file, maxBytes+1)
	}
	return engine.EvidenceFile{Rel: rel, Size: after.Size(), Reader: readCloser{Reader: reader, Closer: file}}, nil
}

type readCloser struct {
	io.Reader
	io.Closer
}

func resolveEvidence(root, candidate string) (evalRoot, evalPath, rel string, info os.FileInfo, err error) {
	if strings.TrimSpace(root) == "" {
		return "", "", "", nil, errors.New("workspace root is required")
	}
	evalRoot, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("resolve workspace: %w", err)
	}
	if candidate == "" || filepath.IsAbs(candidate) {
		return "", "", "", nil, fmt.Errorf("invalid evidence path %q", candidate)
	}
	clean := filepath.Clean(candidate)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", "", nil, fmt.Errorf("evidence path escapes workspace: %q", candidate)
	}
	resolved := filepath.Join(evalRoot, clean)
	evalPath, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("resolve evidence path: %w", err)
	}
	rel, err = filepath.Rel(evalRoot, evalPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", "", nil, fmt.Errorf("evidence path escapes workspace: %q", candidate)
	}
	info, err = os.Lstat(evalPath)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("stat evidence: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", "", "", nil, fmt.Errorf("evidence path is not a regular file: %q", candidate)
	}
	return evalRoot, evalPath, rel, info, nil
}

type evidenceManifest struct {
	Artifacts []engine.Artifact `json:"artifacts"`
}

// MaterializeEvidence writes the verifier evidence bundle and returns its
// cleanup function. Artifact bytes and metadata are hash-verified while the
// bundle is written.
func (OSWorkspace) MaterializeEvidence(ctx context.Context, root string, artifacts []engine.Artifact, open func(context.Context, string) (io.ReadCloser, error)) (string, func() error, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp(root, "verifier-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() error { return os.RemoveAll(dir) }
	for _, artifact := range artifacts {
		if len(artifact.ID) < 2 {
			_ = cleanup()
			return "", nil, fmt.Errorf("artifact %q has an invalid id", artifact.ID)
		}
		reader, err := open(ctx, artifact.ID)
		if err != nil {
			_ = cleanup()
			return "", nil, fmt.Errorf("open artifact %s: %w", artifact.ID, err)
		}
		name := filepath.Base(artifact.Name)
		if name == "." || name == string(filepath.Separator) {
			name = artifact.ID
		}
		target := filepath.Join(dir, artifact.ID[:2], artifact.ID, artifact.AttemptID, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			_ = reader.Close()
			_ = cleanup()
			return "", nil, err
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = reader.Close()
			_ = cleanup()
			return "", nil, err
		}
		hasher := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(file, hasher), reader)
		closeErr := file.Close()
		readerErr := reader.Close()
		if copyErr != nil || closeErr != nil || readerErr != nil {
			_ = cleanup()
			return "", nil, fmt.Errorf("materialize artifact %s", artifact.ID)
		}
		if err := engine.VerifyArtifactBytes(artifact, hasher, written); err != nil {
			_ = cleanup()
			return "", nil, err
		}
	}
	data, err := json.MarshalIndent(evidenceManifest{Artifacts: artifacts}, "", "  ")
	if err != nil {
		_ = cleanup()
		return "", nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o600); err != nil {
		_ = cleanup()
		return "", nil, err
	}
	return dir, cleanup, nil
}
