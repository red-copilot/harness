package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

type testClock struct{}

var testClockTick atomic.Int64

func (testClock) Now() time.Time {
	return time.Unix(1700000000, testClockTick.Add(1)*int64(time.Millisecond)).UTC()
}

type testSecrets struct{}

func (testSecrets) LookupEnv(name string) (string, bool) { return os.LookupEnv(name) }

type testRunDirs struct {
	root   string
	task   string
	skills string
}

func (d testRunDirs) Root() string                       { return d.root }
func (d testRunDirs) TaskDir() string                    { return d.task }
func (d testRunDirs) SkillsDir() string                  { return d.skills }
func (d testRunDirs) SessionDir(sessionID string) string { return filepath.Join(d.root, sessionID) }

func (d testRunDirs) PrepareSession(_ context.Context, sessionID string) (string, error) {
	dir := d.SessionDir(sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

type testWorkspace struct{}

func (testWorkspace) PrepareRun(_ context.Context, root, runID string, materializeSkills func(string) error) (RunDirs, error) {
	runRoot := filepath.Join(root, runID)
	taskDir := filepath.Join(runRoot, "task")
	skillsDir := filepath.Join(runRoot, "skills")
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(skillsDir, 0o700); err != nil {
		return nil, err
	}
	if materializeSkills != nil {
		if err := materializeSkills(skillsDir); err != nil {
			return nil, err
		}
	}
	return testRunDirs{root: runRoot, task: taskDir, skills: skillsDir}, nil
}

func (testWorkspace) ClearReport(_ context.Context, workspaceDir string) error {
	if workspaceDir == "" {
		return nil
	}
	err := os.Remove(filepath.Join(workspaceDir, ReportFileName))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (testWorkspace) ReadReport(_ context.Context, workspaceDir string, maxBytes int64) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(workspaceDir, ReportFileName))
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("report exceeds %d bytes", maxBytes)
	}
	return data, nil
}

func (testWorkspace) OpenEvidence(_ context.Context, workspaceDir, relPath string, maxBytes int64) (EvidenceFile, error) {
	clean := filepath.Clean(relPath)
	file, err := os.Open(filepath.Join(workspaceDir, clean))
	if err != nil {
		return EvidenceFile{}, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return EvidenceFile{}, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return EvidenceFile{}, errors.New("evidence is not a regular file")
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		_ = file.Close()
		return EvidenceFile{}, fmt.Errorf("evidence file exceeds byte limit")
	}
	return EvidenceFile{Rel: clean, Size: info.Size(), Reader: file}, nil
}

func (testWorkspace) MaterializeEvidence(_ context.Context, root string, _ []Artifact, _ func(context.Context, string) (io.ReadCloser, error)) (string, func() error, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp(root, "verifier-")
	if err != nil {
		return "", nil, err
	}
	return dir, func() error { return os.RemoveAll(dir) }, nil
}
