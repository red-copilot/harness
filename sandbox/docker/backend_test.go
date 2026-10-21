package docker_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/sandbox/docker"
)

// TestImageBackendKeepsSecretValuesOutOfArgv proves target environment values
// reach the container through the Docker client environment, not argv, which
// any local process can read.
func TestImageBackendKeepsSecretValuesOutOfArgv(t *testing.T) {
	dir := t.TempDir()
	argvPath := filepath.Join(dir, "argv")
	envPath := filepath.Join(dir, "env")
	t.Setenv("FAKE_DOCKER_ARGV", argvPath)
	t.Setenv("FAKE_DOCKER_ENV", envPath)
	dockerPath := filepath.Join(dir, "docker")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FAKE_DOCKER_ARGV\"\nprintf '%s' \"$TARGET_SECRET\" > \"$FAKE_DOCKER_ENV\"\necho fake-container\n"
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	secret := "s3cr3t-target-value"
	backend := docker.ImageBackend{
		Docker:  dockerPath,
		Image:   "kali:local",
		Network: "host",
		Name:    "harness_target_test",
		Env:     map[string]string{"TARGET_SECRET": secret},
	}
	if _, err := backend.Provision(context.Background(), harness.ProvisionRequest{RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	argv, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(argv), secret) {
		t.Fatalf("secret value appeared in docker argv:\n%s", argv)
	}
	if !strings.Contains(string(argv), "-e\nTARGET_SECRET\n") {
		t.Fatalf("docker argv does not pass the variable by name:\n%s", argv)
	}
	value, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != secret {
		t.Fatalf("docker client env value = %q, want the secret", value)
	}
}
