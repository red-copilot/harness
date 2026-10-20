package docker

import (
	"bufio"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

func TestDockerStarterRoundTrip(t *testing.T) {
	if os.Getenv("HARNESS_DOCKER_TESTS") != "1" {
		t.Skip("set HARNESS_DOCKER_TESTS=1 to run Docker integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runner := New()
	proc, err := runner.Start(ctx, harness.ProcessSpec{
		Image:   "harness/pi-kali:local",
		Command: []string{"sh", "-c", "read line; printf '%s\\n' \"$line\""},
		Network: "none", Name: "harness_roundtrip_test", ReadOnlyRoot: true,
		PidsLimit: 64, Memory: "128m", CPUs: "1",
		Labels: map[string]string{"harness.test": "roundtrip"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := io.WriteString(proc.Stdin(), "hello\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	line, err := bufio.NewReader(proc.Stdout()).ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.TrimSpace(line) != "hello" {
		t.Fatalf("line = %q", line)
	}
	_ = proc.Stdin().Close()
	if _, err := proc.Wait(ctx); err != nil {
		t.Fatalf("wait: %v", err)
	}
}
