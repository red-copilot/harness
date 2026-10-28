package core

import (
	"fmt"
	"net/url"
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

// TargetSpec is the caller-owned, stable authorization identity of one logical
// attack surface. Runtime details belong to TargetBinding.
type TargetSpec struct {
	ID        string        `json:"id" yaml:"id"`
	Addresses []string      `json:"addresses,omitempty" yaml:"addresses,omitempty"`
	Isolation IsolationMode `json:"isolation" yaml:"isolation"`
}

// TargetBinding is the per-Run realization of a TargetSpec. TargetID must
// remain equal to the authorized TargetSpec.ID; only addresses and runtime
// details may be added by a provisioner.
type TargetBinding struct {
	TargetID  string         `json:"target_id"`
	Addresses []string       `json:"addresses,omitempty"`
	Isolation IsolationMode  `json:"isolation"`
	Runtime   *TargetRuntime `json:"runtime,omitempty"`
}

// TargetFingerprint is the canonical, code-owned identity of a logical
// Target. Runtime names and per-Run addresses never contribute to identity.
func TargetFingerprint(target TargetSpec) string {
	return strings.TrimSpace(target.ID)
}

// ValidateTargetBinding rejects a provisioner result that changes authorization
// identity or isolation.
func ValidateTargetBinding(spec TargetSpec, binding TargetBinding) error {
	id := TargetFingerprint(spec)
	if id == "" {
		return fmt.Errorf("target.id is required")
	}
	if strings.TrimSpace(binding.TargetID) != id {
		return fmt.Errorf("target binding id %q does not match authorized target %q", binding.TargetID, id)
	}
	isolation := binding.Isolation
	if isolation == "" {
		isolation = spec.Isolation
	}
	if isolation != spec.Isolation {
		return fmt.Errorf("target binding isolation %q does not match authorized isolation %q", isolation, spec.Isolation)
	}
	return nil
}

// AuthorizeTarget reports whether value references the effective target by id
// or by one of its authorized addresses/hosts.
func AuthorizeTarget(value string, target TargetBinding) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.EqualFold(value, target.TargetID) {
		return true
	}
	valueHost := targetHost(value)
	for _, address := range target.Addresses {
		if strings.EqualFold(value, address) {
			return true
		}
		if valueHost != "" && strings.EqualFold(valueHost, targetHost(address)) {
			return true
		}
	}
	return false
}

func targetHost(value string) string {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "://") {
		if parsed, err := url.Parse(value); err == nil {
			return strings.ToLower(parsed.Hostname())
		}
	}
	if strings.Contains(value, ":") {
		if parsed, err := url.Parse("//" + value); err == nil {
			return strings.ToLower(parsed.Hostname())
		}
	}
	return strings.ToLower(value)
}
