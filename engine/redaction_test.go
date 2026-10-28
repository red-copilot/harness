package engine

import "testing"

func TestRedactorForSpecIncludesInlineEnvAndKnownSecrets(t *testing.T) {
	const envSecret = "env-secret-value"
	t.Setenv("HARNESS_TEST_SECRET", envSecret)
	spec := RunSpec{
		SecretEnv: []string{"HARNESS_TEST_SECRET", "PLANNER_SECRET", "EXECUTOR_SECRET"},
		Reasoner:  AgentSpec{Env: map[string]string{"PLANNER_SECRET": "planner-inline-secret", "MODE": "1"}},
		Executors: []AgentSpec{
			{Env: map[string]string{"EXECUTOR_SECRET": "executor-inline-secret"}},
			{},
		},
		KnownSecrets: []string{"adapter-secret-value"},
	}

	redactor := redactorForSpec(spec)
	for _, secret := range []string{envSecret, "planner-inline-secret", "executor-inline-secret", "adapter-secret-value"} {
		if !redactor.Contains(secret) {
			t.Fatalf("redactor does not know %q", secret)
		}
	}
	if got := redactor.Redact("mode 1"); got != "mode 1" {
		t.Fatalf("nonsecret inline env value was redacted: %q", got)
	}
}
