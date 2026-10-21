package harness

import (
	"strings"
)

// Mount describes a host path exposed to an agent container.
type Mount struct {
	Source      string `json:"source" yaml:"source"`
	Destination string `json:"destination" yaml:"destination"`
	ReadOnly    bool   `json:"read_only,omitempty" yaml:"read_only,omitempty"`
}

// Task is the caller-owned objective. Additional inputs and skills are
// supplied as mounts on the agent specs; the Task itself carries identity and
// intent only.
type Task struct {
	ID        string `json:"id" yaml:"id"`
	Objective string `json:"objective" yaml:"objective"`
}

// IsolationMode records who owns the network boundary. v1 supports only
// caller-managed isolation.
type IsolationMode string

const IsolationCallerManaged IsolationMode = "caller_managed"

// TargetRuntime is adapter-owned, per-Run execution detail: the container,
// network or challenge instance that realizes the Target. It is deliberately
// not identity (see TargetFingerprint), never crosses Runs, and is never an
// authority for the outcome.
type TargetRuntime struct {
	ContainerName  string `json:"container_name,omitempty" yaml:"container_name,omitempty"`
	Network        string `json:"network,omitempty" yaml:"network,omitempty"`
	ComposeProject string `json:"compose_project,omitempty" yaml:"compose_project,omitempty"`
	ComposeFile    string `json:"compose_file,omitempty" yaml:"compose_file,omitempty"`
	// ComposeOverride is the generated file that adds harness labels to the
	// project's services; Close removes it.
	ComposeOverride string `json:"compose_override,omitempty" yaml:"compose_override,omitempty"`
	ChallengeCode   string `json:"challenge_code,omitempty" yaml:"challenge_code,omitempty"`
	BenchmarkURL    string `json:"benchmark_base_url,omitempty" yaml:"benchmark_base_url,omitempty"`
	Hint            string `json:"hint,omitempty" yaml:"hint,omitempty"`
	HintUsed        bool   `json:"hint_used,omitempty" yaml:"hint_used,omitempty"`
}

// Target is one logical authorized attack surface.
type Target struct {
	ID        string         `json:"id" yaml:"id"`
	Addresses []string       `json:"addresses,omitempty" yaml:"addresses,omitempty"`
	Isolation IsolationMode  `json:"isolation" yaml:"isolation"`
	Runtime   *TargetRuntime `json:"runtime,omitempty" yaml:"runtime,omitempty"`
}

// TargetFingerprint is the canonical, code-owned identity of a logical
// Target. Runtime names such as container or compose project names are per-Run
// and never identity, so Knowledge reuse always compares fingerprints.
func TargetFingerprint(target Target) string {
	return strings.TrimSpace(target.ID)
}
