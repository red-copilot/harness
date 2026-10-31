package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/store/sqlite"
)

func TestCLIComposesReasonerExecutorAndAdapters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.yaml")
	data := `apiVersion: harness/v4
task:
  id: task-1
  objective: test target
target:
  id: target-1
  addresses: [10.0.0.1:80]
  isolation: caller_managed
reasoner:
  id: reasoner
  runtime: {provider: test, model: plan}
executor:
  id: exec-1
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
	if spec.Reasoner.ID != "reasoner" || spec.Executor.ID != "exec-1" || spec.Reasoner.Runtime == nil || spec.Executor.Runtime == nil {
		t.Fatalf("spec = %+v", spec)
	}
	if spec.Target.Isolation != harness.IsolationCallerManaged || spec.Artifacts == nil {
		t.Fatalf("binding = %+v", spec)
	}
	if spec.Policy.Budget.WallTime != 60*time.Second {
		t.Fatalf("budget = %+v, want 60s", spec.Policy.Budget)
	}
}

func TestCLIRejectsV1AndLegacyKeys(t *testing.T) {
	for _, key := range []string{"workflow", "pi", "agents", "targets", "exploration", "planner"} {
		path := filepath.Join(t.TempDir(), key+".yaml")
		if err := os.WriteFile(path, []byte("apiVersion: harness/v4\n"+key+": {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil {
			t.Fatalf("legacy key %q accepted", key)
		}
	}
}

func TestCLIRejectsUnknownPolicyKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "typo.yaml")
	data := `apiVersion: harness/v4
task: {id: task-1, objective: test}
target: {id: target-1}
reasoner: {id: reasoner}
executor: {id: exec-1}
policy:
  max_dept: 3
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("misspelled policy key was silently ignored")
	}
}

func TestCLIDefaultsEachPolicyFieldIndependently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.yaml")
	data := `apiVersion: harness/v4
task: {id: task-1, objective: test}
target: {id: target-1}
reasoner: {id: reasoner}
executor: {id: exec-1}
policy:
  max_total_intents: 7
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
	defaults := harness.DefaultRunPolicy()
	if spec.Policy.Exploration.MaxTotalIntents != 7 {
		t.Fatalf("max_total_intents = %d, want 7", spec.Policy.Exploration.MaxTotalIntents)
	}
	if spec.Policy.Exploration.MaxAttemptsPerIntent != defaults.Exploration.MaxAttemptsPerIntent {
		t.Fatalf("max_attempts_per_intent = %d, want default %d", spec.Policy.Exploration.MaxAttemptsPerIntent, defaults.Exploration.MaxAttemptsPerIntent)
	}
}
