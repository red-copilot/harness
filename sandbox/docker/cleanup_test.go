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
	target := harness.Target{Runtime: &harness.TargetRuntime{ContainerName: "c", Network: "n"}}
	if err := backend.Close(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if err := backend.Close(context.Background(), target); err != nil {
		t.Fatal(err)
	}
}

// TestCleanupOrphansIsOwnerScoped: a second harness process sharing the Docker
// daemon must not reclaim resources that belong to a live deployment.
func TestCleanupOrphansIsOwnerScoped(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "docker.log")
	t.Setenv("FAKE_DOCKER_LOG", logPath)
	script := filepath.Join(dir, "docker")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$FAKE_DOCKER_LOG\"\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Docker: script, Owner: "node-a"}
	if err := runner.CleanupOrphans(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "label=harness.managed=true") {
		t.Fatalf("cleanup no longer selects managed resources:\n%s", data)
	}
	if !strings.Contains(string(data), "label=harness.owner=node-a") {
		t.Fatalf("cleanup is not owner-scoped:\n%s", data)
	}
}
