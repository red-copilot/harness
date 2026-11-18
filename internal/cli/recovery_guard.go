package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// recoveryGuard returns a guard that holds an exclusive OS-level file lock
// next to the database for the lifetime of the returned release function.
// Marking interrupted runs is refused while another live process holds the
// lock, so a second CLI process cannot clobber a live runner's runs. The lock
// is released by the OS if the holder crashes, so no heartbeat is needed.
func recoveryGuard(dbPath string) (func() error, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	lockPath := dbPath + ".lock"
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("another harness process holds %s; refusing to touch its runs: %w", lockPath, err)
	}
	release := func() error {
		defer file.Close()
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
			return fmt.Errorf("release lock: %w", err)
		}
		return nil
	}
	return release, nil
}
