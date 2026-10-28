package engine

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/red-copilot/harness/core"
)

// Config configures a Runner.
type Config struct {
	Store         Store
	Starter       ProcessStarter
	WorkspaceRoot string
	MaxSessions   int
	PollInterval  time.Duration
	CloseTimeout  time.Duration
	Cleaner       ResourceCleaner
	SkipRecovery  bool
}

// RunSpec binds a serializable descriptor to runtime adapter instances.
type RunSpec struct {
	Task            core.Task
	Target          core.TargetSpec
	Reasoner        AgentSpec
	Executors       []AgentSpec
	Policy          core.RunPolicy
	ReasonStrategy  core.ReasonStrategy
	ExploreStrategy core.ExploreStrategy
	Provisioner     TargetProvisioner
	Backend         CompletionSource
	Verifier        Verifier
	Artifacts       ArtifactStore
	SecretEnv       []string
	KnownSecrets    []string
	WorkspaceRoot   string
}

// ValidateRunSpec validates adapter-bound Run inputs before persistence.
func ValidateRunSpec(spec RunSpec) error {
	if err := core.ValidateRunPolicy(core.NormalizeRunPolicy(spec.Policy)); err != nil {
		return fmt.Errorf("policy: %w", err)
	}
	if strings.TrimSpace(spec.Task.ID) == "" {
		return errors.New("task.id is required")
	}
	if strings.TrimSpace(spec.Task.Objective) == "" {
		return errors.New("task.objective is required")
	}
	if strings.TrimSpace(spec.Target.ID) == "" {
		return errors.New("target.id is required")
	}
	isolation := spec.Target.Isolation
	if isolation == "" {
		isolation = core.IsolationCallerManaged
	}
	if isolation != core.IsolationCallerManaged {
		return fmt.Errorf("target.isolation %q is unsupported", isolation)
	}
	if spec.Reasoner.Runtime == nil {
		return errors.New("reasoner.runtime is required")
	}
	if len(spec.Executors) != 2 {
		return errors.New("exactly 2 executors are required")
	}
	seen := map[string]struct{}{}
	for i, executor := range spec.Executors {
		if strings.TrimSpace(executor.ID) == "" {
			return fmt.Errorf("executors[%d].id is required", i)
		}
		if _, ok := seen[executor.ID]; ok {
			return fmt.Errorf("executors[%d].id is duplicated", i)
		}
		seen[executor.ID] = struct{}{}
		if executor.Runtime == nil {
			return fmt.Errorf("executors[%d].runtime is required", i)
		}
	}
	if spec.Artifacts == nil {
		return errors.New("artifacts store is required")
	}
	if spec.Verifier == nil && spec.Backend == nil {
		return errors.New("authority is required: verifier or backend")
	}
	return nil
}

// Descriptor projects the serializable portion of a RunSpec.
func (spec RunSpec) Descriptor() core.RunDescriptor {
	executors := make([]core.AgentDescriptor, len(spec.Executors))
	for i := range spec.Executors {
		executors[i] = spec.Executors[i].Descriptor()
	}
	return core.RunDescriptor{
		SchemaVersion: core.CurrentSchemaVersion,
		Task:          spec.Task,
		Target:        spec.Target,
		Reasoner:      spec.Reasoner.Descriptor(),
		Executors:     executors,
		Policy:        core.NormalizeRunPolicy(spec.Policy),
	}
}
