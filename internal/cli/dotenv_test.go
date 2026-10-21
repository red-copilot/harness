package cli

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/red-copilot/harness"
)

func TestLoadEnvFileSetsMissingValues(t *testing.T) {
	t.Setenv("HARNESS_DOTENV_TEST_MISSING", "")
	unsetEnv(t, "HARNESS_DOTENV_TEST_MISSING")

	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("HARNESS_DOTENV_TEST_MISSING=loaded\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadEnvFile(path); err != nil {
		t.Fatalf("load env file: %v", err)
	}
	if got := os.Getenv("HARNESS_DOTENV_TEST_MISSING"); got != "loaded" {
		t.Fatalf("value = %q, want %q", got, "loaded")
	}
}

func TestLoadEnvFileDoesNotOverrideExplicitEnvironment(t *testing.T) {
	t.Setenv("HARNESS_DOTENV_TEST_EXPLICIT", "explicit")

	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("HARNESS_DOTENV_TEST_EXPLICIT=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadEnvFile(path); err != nil {
		t.Fatalf("load env file: %v", err)
	}
	if got := os.Getenv("HARNESS_DOTENV_TEST_EXPLICIT"); got != "explicit" {
		t.Fatalf("value = %q, want explicit environment to win", got)
	}
}

func TestMainLoadsDotEnvFromWorkingDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HARNESS_DOTENV_TEST_MAIN", "")
	unsetEnv(t, "HARNESS_DOTENV_TEST_MAIN")

	if err := os.WriteFile(".env", []byte("HARNESS_DOTENV_TEST_MAIN=loaded\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := Main([]string{"version"}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("Main returned %d, want 0", code)
	}
	if got := os.Getenv("HARNESS_DOTENV_TEST_MAIN"); got != "loaded" {
		t.Fatalf("value = %q, want %q", got, "loaded")
	}
}

func TestApplyPiEnvironmentDefaults(t *testing.T) {
	t.Setenv("PI_PROVIDER", "opencode-go")
	t.Setenv("PI_MODEL", "minimax-m3.1-flash-free")
	spec := harness.RunSpec{Agents: []harness.AgentSpec{{
		ID: "pi",
		Pi: &harness.PiSpec{},
	}}}

	applyPiEnvironmentDefaults(&spec)

	got := spec.Agents[0].Pi
	if got.Provider != "opencode-go" || got.Model != "minimax-m3.1-flash-free" {
		t.Fatalf("provider/model = %q/%q", got.Provider, got.Model)
	}
}

func TestApplyPiEnvironmentDefaultsPreservesExplicitValues(t *testing.T) {
	t.Setenv("PI_PROVIDER", "environment-provider")
	t.Setenv("PI_MODEL", "environment-model")
	spec := harness.RunSpec{Agents: []harness.AgentSpec{{
		ID: "pi",
		Pi: &harness.PiSpec{Provider: "explicit-provider", Model: "explicit-model"},
	}}}

	applyPiEnvironmentDefaults(&spec)

	got := spec.Agents[0].Pi
	if got.Provider != "explicit-provider" || got.Model != "explicit-model" {
		t.Fatalf("provider/model = %q/%q, want explicit values", got.Provider, got.Model)
	}
}

func unsetEnv(t *testing.T, name string) {
	t.Helper()
	value, present := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if present {
			_ = os.Setenv(name, value)
			return
		}
		_ = os.Unsetenv(name)
	})
}
