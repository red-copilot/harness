// Package lease provides the cross-process writer lease used by the v5 store.
package lease

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrHeld means another process already holds the lease.
var ErrHeld = errors.New("lease held")

// Handle is one held lease. Release is idempotent.
type Handle struct {
	path    string
	release func() error
}

// Acquire takes an exclusive, non-blocking lease at path.
func Acquire(path string) (*Handle, error) {
	if path == "" {
		return nil, errors.New("lease path is required")
	}
	release, err := acquire(path)
	if err != nil {
		return nil, err
	}
	return &Handle{path: path, release: release}, nil
}

// Path returns the leased path.
func (h *Handle) Path() string {
	if h == nil {
		return ""
	}
	return h.path
}

// Release releases the lease.
func (h *Handle) Release() error {
	if h == nil || h.release == nil {
		return nil
	}
	release := h.release
	h.release = nil
	return release()
}

func ensureDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "." {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create lease directory: %w", err)
	}
	return nil
}

// writeDiagnostics is best-effort operator context; correctness comes from the
// OS lock, not from this content.
func writeDiagnostics(file *os.File) {
	host, _ := os.Hostname()
	line := fmt.Sprintf("pid=%d host=%s started=%s\n", os.Getpid(), host, time.Now().UTC().Format(time.RFC3339))
	_ = file.Truncate(0)
	_, _ = file.WriteAt([]byte(line), 0)
}
