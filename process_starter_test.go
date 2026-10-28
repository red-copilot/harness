package harness_test

import (
	"context"
	"io"
	"strings"

	"github.com/red-copilot/harness"
)

// noopStarter satisfies the Runner composition contract for tests that use
// fake agent runtimes and never start a real process.
type noopStarter struct{}

func (noopStarter) Start(context.Context, harness.ProcessSpec) (harness.Process, error) {
	return noopProcess{}, nil
}

type noopProcess struct{}

func (noopProcess) Stdin() io.WriteCloser { return noopWriteCloser{} }
func (noopProcess) Stdout() io.Reader     { return strings.NewReader("") }
func (noopProcess) Stderr() io.Reader     { return strings.NewReader("") }
func (noopProcess) Wait(context.Context) (harness.ExitStatus, error) {
	return harness.ExitStatus{}, nil
}
func (noopProcess) Kill(context.Context) error { return nil }

type noopWriteCloser struct{}

func (noopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (noopWriteCloser) Close() error                { return nil }
