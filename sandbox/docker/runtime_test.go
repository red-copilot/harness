package docker

import (
	"strings"
	"testing"

	"github.com/red-copilot/harness"
)

func TestBuildRunArgsHardensAndScopesContainer(t *testing.T) {
	args := BuildRunArgs(harness.ProcessSpec{
		Image: "harness/pi:test", Command: []string{"pi", "--mode", "rpc"},
		Env: map[string]string{"A": "1"}, Network: "host",
		CapAdd: []string{"NET_RAW"},
		Mounts: []harness.Mount{
			{Source: "/host/task", Destination: "/task", ReadOnly: true},
			{Source: "/host/ws", Destination: "/workspace"},
		},
		Name: "harness_pi_test", Labels: map[string]string{"harness.run_id": "r1", "harness.session_id": "s1"},
		WorkingDir: "/workspace", User: "1000:1000", ReadOnlyRoot: true, PidsLimit: 256, Memory: "2g", CPUs: "2",
	})
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"run --rm -i", "--name harness_pi_test", "--label harness.managed=true",
		"--label harness.run_id=r1", "--network host", "--read-only",
		"--cap-drop ALL", "--cap-add NET_RAW", "--security-opt no-new-privileges",
		"--pids-limit 256", "--memory 2g", "--cpus 2",
		"-v /host/task:/task:ro", "-v /host/ws:/workspace",
		"-w /workspace", "--user 1000:1000", "-e A", "harness/pi:test pi --mode rpc",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q missing %q", joined, want)
		}
	}
	if strings.Contains(joined, "docker.sock") {
		t.Fatal("docker socket must never be mounted")
	}
}

func TestBuildRunArgsRejectsMissingImage(t *testing.T) {
	if _, err := BuildRunArgsChecked(harness.ProcessSpec{}); err == nil {
		t.Fatal("expected missing image error")
	}
}
