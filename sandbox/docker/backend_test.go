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

// TestDockerProvisionersAreNotAuthorities: starting or owning a Target is not
// evidence of the security outcome, so the Docker adapters must not satisfy
// the authoritative completion port.
func TestDockerProvisionersAreNotAuthorities(t *testing.T) {
	for _, backend := range []any{docker.ExternalBackend{}, docker.ImageBackend{}, docker.ComposeBackend{}} {
		if _, ok := backend.(harness.CompletionSource); ok {
			t.Fatalf("%T claims to be an authoritative completion source", backend)
		}
		if _, ok := backend.(harness.TargetProvisioner); !ok {
			t.Fatalf("%T does not provision a Target", backend)
		}
	}
}

// TestImageBackendLabelsOwner scopes its resources to the deployment so a
// second harness process cannot reclaim them.
func TestImageBackendLabelsOwner(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "argv")
	dockerPath := filepath.Join(dir, "docker")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"" + logPath + "\"\n"
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	backend := docker.ImageBackend{Docker: dockerPath, Image: "kali:local", Network: "host", Name: "harness_target_test", Owner: "node-a"}
	if _, err := backend.Provision(context.Background(), harness.ProvisionRequest{RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "harness.owner=node-a") {
		t.Fatalf("target resources are not owner-scoped:\n%s", data)
	}
}

// TestComposeBackendLabelsEveryService: compose has no per-invocation label
// flag, so a generated override must label every declared service. Otherwise a
// crashed project is invisible to orphan cleanup.
func TestComposeBackendLabelsEveryService(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "argv")
	t.Setenv("FAKE_DOCKER_LOG", logPath)
	dockerPath := filepath.Join(dir, "docker")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$FAKE_DOCKER_LOG\"\ncase \"$*\" in *\"config --services\"*) printf 'web\\napi\\n' ;; esac\nexit 0\n"
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	composeFile := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(composeFile, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := docker.ComposeBackend{Docker: dockerPath, File: composeFile, Owner: "node-a"}
	target, err := backend.Provision(context.Background(), harness.ProvisionRequest{RunID: "run-1"})
	if err != nil {
		t.Fatal(err)
	}
	if target.Runtime == nil || target.Runtime.ComposeOverride == "" {
		t.Fatalf("compose target runtime = %#v", target.Runtime)
	}
	override, err := os.ReadFile(target.Runtime.ComposeOverride)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"web"`, `"api"`, "harness.managed", "harness.owner", "node-a", "run-1"} {
		if !strings.Contains(string(override), want) {
			t.Fatalf("compose override lacks %q:\n%s", want, override)
		}
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), target.Runtime.ComposeOverride) {
		t.Fatalf("compose up did not use the label override:\n%s", data)
	}
	if err := backend.Close(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target.Runtime.ComposeOverride); !os.IsNotExist(err) {
		t.Fatalf("compose override survived teardown: %v", err)
	}
}
