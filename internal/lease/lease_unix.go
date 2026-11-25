//go:build unix

package lease

import (
	"fmt"
	"os"
	"syscall"
)

func acquire(path string) (func() error, error) {
	if err := ensureDir(path); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lease file: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("%w: %s: %v", ErrHeld, path, err)
	}
	writeDiagnostics(file)
	return func() error {
		unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		closeErr := file.Close()
		if unlockErr != nil {
			return fmt.Errorf("release lease: %w", unlockErr)
		}
		return closeErr
	}, nil
}
