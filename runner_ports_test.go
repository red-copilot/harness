package harness_test

import (
	"testing"

	"github.com/red-copilot/harness"
)

func TestNewRunnerRequiresInjectedKernelPorts(t *testing.T) {
	store := newMemoryStore()
	cases := []struct {
		name string
		cfg  harness.Config
	}{
		{name: "clock", cfg: harness.Config{Store: store, IDs: testIDs{}, Secrets: testSecrets{}}},
		{name: "ids", cfg: harness.Config{Store: store, Clock: testClock{}, Secrets: testSecrets{}}},
		{name: "secrets", cfg: harness.Config{Store: store, Clock: testClock{}, IDs: testIDs{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := harness.NewRunner(tc.cfg); err == nil {
				t.Fatalf("NewRunner accepted missing %s port", tc.name)
			}
		})
	}
}
