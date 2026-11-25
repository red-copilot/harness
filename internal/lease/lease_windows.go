//go:build windows

package lease

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func acquire(path string) (func() error, error) {
	if err := ensureDir(path); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lease file: %w", err)
	}
	handle := windows.Handle(file.Fd())
	overlapped := new(windows.Overlapped)
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("%w: %s: %v", ErrHeld, path, err)
	}
	writeDiagnostics(file)
	return func() error {
		unlockErr := windows.UnlockFileEx(handle, 0, 1, 0, overlapped)
		closeErr := file.Close()
		if unlockErr != nil {
			return fmt.Errorf("release lease: %w", unlockErr)
		}
		return closeErr
	}, nil
}
