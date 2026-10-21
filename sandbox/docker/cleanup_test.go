package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/red-copilot/harness"
)

func TestDockerCleanupRunsAfterProvisionFailure(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "docker.log")
	script := filepath.Join(dir, "docker")
	content := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + logPath + "\"\nif [ \"$1\" = run ]; then echo target failed >&2; exit 9; fi\nexit 0\n"
	if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	backend := ImageBackend{Docker: script, Image: "target:test"}
	if _, err := backend.Provision(context.Background(), harness.ProvisionRequest{RunID: "run-1"}); err == nil {
		t.Fatal("expected provision failure")
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "network rm") {
		t.Fatalf("network was not cleaned up: %s", data)
	}
}

func TestDockerCloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "docker")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	backend := ImageBackend{Docker: script}
	target := harness.Target{Metadata: map[string]string{"container_name": "c", "network": "n"}}
	if err := backend.Close(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if err := backend.Close(context.Background(), target); err != nil {
		t.Fatal(err)
	}
}
