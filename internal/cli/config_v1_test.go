package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/store/sqlite"
)

func TestCLIComposesPlannerExecutorsAndAdapters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.yaml")
	data := `apiVersion: harness/v1
task:
  id: task-1
  objective: test target
target:
  id: target-1
  addresses: [10.0.0.1:80]
  isolation: caller_managed
planner:
  id: planner
  runtime: {provider: test, model: plan}
executors:
  - id: exec-1
  - id: exec-2
budget:
  wall_time_seconds: 60
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	spec, err := cfg.ToRunSpec(store)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Planner.ID != "planner" || len(spec.Executors) != 2 || spec.Planner.Runtime == nil || spec.Executors[0].Runtime == nil {
		t.Fatalf("spec = %+v", spec)
	}
	if spec.Target.Isolation != harness.IsolationCallerManaged || spec.Artifacts == nil {
		t.Fatalf("binding = %+v", spec)
	}
}

func TestCLIRejectsWorkflowAndPiSpecKeys(t *testing.T) {
	for _, key := range []string{"workflow", "pi", "agents", "targets", "exploration"} {
		path := filepath.Join(t.TempDir(), key+".yaml")
		if err := os.WriteFile(path, []byte("apiVersion: harness/v1\n"+key+": {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil {
			t.Fatalf("legacy key %q accepted", key)
		}
	}
}

func TestApplyPiEnvironmentDefaults(t *testing.T) {
	t.Setenv("PI_PROVIDER", "provider-env")
	t.Setenv("PI_MODEL", "model-env")
	descriptor := harness.RunDescriptor{Planner: harness.AgentSpec{ID: "planner"}, Executors: []harness.AgentSpec{{ID: "exec"}}}
	ApplyPiEnvironmentDefaults(&descriptor)
	if len(descriptor.Planner.RuntimeConfig) == 0 || len(descriptor.Executors[0].RuntimeConfig) == 0 {
		t.Fatal("runtime config was not populated")
	}
	_ = context.Background()
}

func TestCLIComposesTSecBackend(t *testing.T) {
	t.Setenv("BENCHMARK_BASE_URL", "https://tsec.example")
	t.Setenv("BENCHMARK_TOKEN", "test-token")
	cfg := &FileConfig{
		APIVersion: "harness/v1",
		Task:       harness.Task{ID: "task", Objective: "solve"},
		Target:     TargetFileConfig{Kind: "tsec", ID: "web-1", Isolation: string(harness.IsolationCallerManaged)},
		Planner:    AgentFileSpec{ID: "planner"},
		Executors:  []AgentFileSpec{{ID: "exec"}},
		TSec:       &TSecFileConfig{BaseURLEnv: "BENCHMARK_BASE_URL", TokenEnv: "BENCHMARK_TOKEN", CheckVPN: false},
	}
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	spec, err := cfg.ToRunSpec(store)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Backend == nil {
		t.Fatal("TSec backend not composed")
	}
	if len(spec.Executors[0].EnvFrom) == 0 || spec.SecretEnv[0] != "BENCHMARK_TOKEN" {
		t.Fatalf("credential binding = %+v secret=%v", spec.Executors[0].EnvFrom, spec.SecretEnv)
	}
}
