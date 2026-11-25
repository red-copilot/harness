package lease

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestAcquireIsExclusiveAndReleasable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.db.lock")
	first, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(path); !errors.Is(err, ErrHeld) {
		t.Fatalf("second acquire err = %v, want ErrHeld", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}
