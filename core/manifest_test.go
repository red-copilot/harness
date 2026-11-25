package core

import (
	"strings"
	"testing"
	"time"
)

func testManifest() Manifest {
	return Manifest{
		SchemaVersion: ManifestSchemaVersion,
		RunID:         "run-1",
		Task:          Task{ID: "task-1", Objective: "prove the target is reachable"},
		Target:        TargetSpec{ID: "target-1", Addresses: []string{"10.0.0.1:80"}, Isolation: IsolationCallerManaged},
		Policy:        DefaultRunPolicy(),
		Reasoner:      AgentDescriptor{ID: "reasoner"},
		Executor:      AgentDescriptor{ID: "executor"},
		Verifier:      VerifierDescriptor{ID: "verifier", ConfigDigest: "config-v1"},
		CreatedAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestManifestValidate(t *testing.T) {
	if err := testManifest().Validate(); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"schema", func(m *Manifest) { m.SchemaVersion = 4 }},
		{"run id", func(m *Manifest) { m.RunID = "" }},
		{"task id", func(m *Manifest) { m.Task.ID = "" }},
		{"objective", func(m *Manifest) { m.Task.Objective = " " }},
		{"target", func(m *Manifest) { m.Target.ID = "" }},
		{"isolation", func(m *Manifest) { m.Target.Isolation = "host" }},
		{"reasoner", func(m *Manifest) { m.Reasoner.ID = "" }},
		{"executor", func(m *Manifest) { m.Executor.ID = "" }},
		{"verifier", func(m *Manifest) { m.Verifier.ID = "" }},
		{"created at", func(m *Manifest) { m.CreatedAt = time.Time{} }},
		{"policy", func(m *Manifest) { m.Policy.Exploration.MaxTotalIntents = -1 }},
		{"budget", func(m *Manifest) { m.Policy.Budget.MaxTokens = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testManifest()
			tc.mutate(&m)
			if err := m.Validate(); err == nil {
				t.Fatalf("manifest accepted: %+v", m)
			}
			if _, err := m.Digest(); err == nil {
				t.Fatal("Digest accepted an invalid manifest")
			}
		})
	}
}

func TestManifestNormalizationIsDigestStable(t *testing.T) {
	explicit := testManifest()
	defaulted := explicit
	defaulted.Target.Isolation = ""
	defaulted.Policy = RunPolicy{}
	first, err := explicit.Digest()
	if err != nil {
		t.Fatal(err)
	}
	second, err := defaulted.Normalized().Digest()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("normalized digests differ: %s vs %s", first, second)
	}
	if defaulted.Normalized().Target.Isolation != IsolationCallerManaged {
		t.Fatalf("isolation = %q", defaulted.Normalized().Target.Isolation)
	}
}

func TestManifestDigestGolden(t *testing.T) {
	digest, err := testManifest().Digest()
	if err != nil {
		t.Fatal(err)
	}
	const want = "57507643823e30b21e58ff54b20bb890607a7641c2f665625a50d00d377b5f52"
	if digest != want {
		t.Fatalf("digest = %s, want %s", digest, want)
	}
}

func TestCanonicalJSONIsDeterministicAndUnescaped(t *testing.T) {
	first, err := CanonicalJSON(map[string]string{"b": "<two>", "a": "one"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := CanonicalJSON(map[string]string{"a": "one", "b": "<two>"})
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("canonical JSON differs: %s vs %s", first, second)
	}
	if !strings.Contains(string(first), "<two>") {
		t.Fatalf("HTML escaping was not disabled: %s", first)
	}
	if strings.HasPrefix(string(first), "{\n") || strings.HasSuffix(string(first), "\n") {
		t.Fatalf("unexpected whitespace: %q", first)
	}
}
