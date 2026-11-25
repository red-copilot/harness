package kernel

import (
	"fmt"
	"runtime/debug"
)

// panicError marks a recovered panic. Unlike a turn failure it is fatal: a
// panic at a goroutine root or in the verifier is a control error and must
// finalize the Run as failed.
type panicError struct{ detail string }

func (e *panicError) Error() string { return "panic: " + e.detail }

func isPanic(err error) bool {
	_, ok := err.(*panicError)
	return ok
}

func capturePanic(err *error) {
	if recovered := recover(); recovered != nil {
		*err = &panicError{detail: fmt.Sprintf("%v\n%s", recovered, debug.Stack())}
	}
}

// runSafely converts a panic at a goroutine root into an error so the Run can
// fail closed with a redacted control error instead of leaving a half-open Run.
func runSafely(fn func() error) (err error) {
	defer capturePanic(&err)
	return fn()
}
