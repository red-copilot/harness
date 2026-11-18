package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

func writeTestConfig(t *testing.T, policyBlock string) *FileConfig {
	t.Helper()
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
policy:
` + policyBlock
	path := filepath.Join(t.TempDir(), "run.yaml")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// The engine-cadence knobs must default independently of the legacy exploration
// keys: a config that leaves them unset gets the core defaults, and each key
// overrides only its own knob.
func TestPolicyEngineCadenceDefaultsAndOverrides(t *testing.T) {
	cfg := writeTestConfig(t, "  stagnation_rounds: 5\n")
	policy := cfg.Policy.toRunPolicy()
	defaults := harness.DefaultRunPolicy()
	if policy.Exploration.EnginePollInterval != defaults.Exploration.EnginePollInterval {
		t.Fatalf("EnginePollInterval = %s, want core default %s", policy.Exploration.EnginePollInterval, defaults.Exploration.EnginePollInterval)
	}
	if policy.Exploration.DrainTimeout != defaults.Exploration.DrainTimeout {
		t.Fatalf("DrainTimeout = %s, want core default %s", policy.Exploration.DrainTimeout, defaults.Exploration.DrainTimeout)
	}
	if policy.Exploration.AbortTimeout != defaults.Exploration.AbortTimeout {
		t.Fatalf("AbortTimeout = %s, want core default %s", policy.Exploration.AbortTimeout, defaults.Exploration.AbortTimeout)
	}
	if policy.Exploration.StagnationRounds != 5 {
		t.Fatalf("StagnationRounds = %d, want explicit 5", policy.Exploration.StagnationRounds)
	}

	cfg = writeTestConfig(t, "  engine_poll_interval: 5ms\n  drain_timeout: 1s\n  abort_timeout: 2s\n")
	policy = cfg.Policy.toRunPolicy()
	if policy.Exploration.EnginePollInterval != 5*time.Millisecond {
		t.Fatalf("EnginePollInterval = %s, want 5ms", policy.Exploration.EnginePollInterval)
	}
	if policy.Exploration.DrainTimeout != time.Second {
		t.Fatalf("DrainTimeout = %s, want 1s", policy.Exploration.DrainTimeout)
	}
	if policy.Exploration.AbortTimeout != 2*time.Second {
		t.Fatalf("AbortTimeout = %s, want 2s", policy.Exploration.AbortTimeout)
	}
	if policy.Exploration.StagnationRounds != defaults.Exploration.StagnationRounds {
		t.Fatalf("StagnationRounds = %d, want untouched default", policy.Exploration.StagnationRounds)
	}
}

func TestPolicyRejectsUnknownCadenceKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.yaml")
	data := `apiVersion: harness/v4
task:
  id: task-1
  objective: test target
target:
  id: target-1
reasoner:
  id: reasoner
executor:
  id: exec-1
policy:
  engine_poll_tick: 5ms
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("unknown policy key accepted")
	}
}
