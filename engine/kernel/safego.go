package kernel

import (
	"fmt"
	"runtime/debug"
)

// runSafely converts a panic at a goroutine root into an error so the Run can
// fail closed with a redacted control error instead of leaving a half-open Run.
func runSafely(fn func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v\n%s", recovered, debug.Stack())
		}
	}()
	return fn()
}
