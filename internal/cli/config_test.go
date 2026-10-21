package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfigConvertsWallTimeAndTSecDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.yaml")
	content := `apiVersion: harness/v1alpha1
task:
  id: demo
  objective: find the flag
agents:
  - id: pi
    image: kali-pi:test
    pi:
      provider: openai
      model: gpt-test
budget:
  wall_time_seconds: 90
secret_env: [CUSTOM_SECRET]
tsec:
  challenge: web_1
  base_url_env: BENCHMARK_BASE_URL
  token_env: BENCHMARK_TOKEN
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	spec, err := cfg.ToRunSpec()
	if err != nil {
		t.Fatalf("ToRunSpec: %v", err)
	}
	if spec.Budget.WallTime != 90*time.Second {
		t.Fatalf("wall time = %s", spec.Budget.WallTime)
	}
	if cfg.TSec == nil || cfg.TSec.Challenge != "web_1" {
		t.Fatalf("tsec = %#v", cfg.TSec)
	}
}

func TestMigrateLegacyTaskAndAgent(t *testing.T) {
	dir := t.TempDir()
	task := filepath.Join(dir, "task.yaml")
	agent := filepath.Join(dir, "agent.yaml")
	out := filepath.Join(dir, "run.json")
	_ = os.WriteFile(task, []byte(`apiVersion: harness/v1
id: legacy
objective:
  description: solve legacy task
`), 0o600)
	_ = os.WriteFile(agent, []byte(`apiVersion: harness/v1
id: pi
image: harness/pi-kali:local
pi:
  provider: openai
  model: gpt-test
`), 0o600)
	if err := Migrate(task, agent, out); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	cfg, err := LoadConfig(out)
	if err != nil {
		t.Fatalf("LoadConfig migrated: %v", err)
	}
	if cfg.Task.ID != "legacy" || cfg.Task.Objective != "solve legacy task" {
		t.Fatalf("task = %#v", cfg.Task)
	}
	if len(cfg.Agents) != 1 || cfg.Agents[0].Pi == nil || cfg.Agents[0].Pi.Model != "gpt-test" {
		t.Fatalf("agents = %#v", cfg.Agents)
	}
}

func TestBundledExamplesLoad(t *testing.T) {
	for _, path := range []string{"../../examples/run.yaml", "../../examples/tsec.yaml"} {
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if _, err := cfg.ToRunSpec(); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
}

func TestSingleAgentConfigEnablesFrontierEngine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.yaml")
	content := `apiVersion: harness/v1alpha1
task:
  id: frontier
  objective: find the flag
agents:
  - id: pi
    image: kali-pi:test
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	spec, err := cfg.ToRunSpec()
	if err != nil {
		t.Fatalf("ToRunSpec: %v", err)
	}
	if spec.Exploration == nil {
		t.Fatal("single-agent config did not enable exploration")
	}
	if spec.Exploration.MaxDepth != 4 || spec.Exploration.MaxNodes != 15 {
		t.Fatalf("tree policy = %+v, want depth 4 nodes 15", spec.Exploration)
	}
	if spec.Exploration.MaxActiveBranches != 2 {
		t.Fatalf("max active branches = %d, want 2", spec.Exploration.MaxActiveBranches)
	}
}
