package cli

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestRecoveryGuardExcludesSecondProcess(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "harness.db")
	release, err := recoveryGuard(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// While one holder owns the lock, every further acquisition is refused.
	if _, err := recoveryGuard(dbPath); err == nil {
		t.Fatal("second acquisition while lock is held accepted")
		_ = release()
		return
	} else if !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("err = %v, want refusal naming the lock", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	// A released lock can be re-acquired.
	release, err = recoveryGuard(dbPath)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryGuardReleasedOnFileDescriptorReuse(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "harness.db")
	release, err := recoveryGuard(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// The release must drop the flock even though the fd stays open until the
	// closure returns; verify with a raw probe afterwards.
	if err := release(); err != nil {
		t.Fatal(err)
	}
	probe, err := syscall.Open(dbPath+".lock", syscall.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(probe)
	if err := syscall.Flock(probe, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	_ = syscall.Flock(probe, syscall.LOCK_UN)
}
