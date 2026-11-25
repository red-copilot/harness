// Package kernel implements the v5 Run kernel on the event-sourced journal.
// It is the transitional home of the v5 control loop while the v4 engine still
// supplies shared protocol helpers; it folds into package engine when the v4
// kernel is deleted.
package kernel

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/red-copilot/harness/core"
	"github.com/red-copilot/harness/engine"
)

// Config configures a v5 Runner.
type Config struct {
	Store           engine.V5Store
	Clock           engine.Clock
	IDs             engine.IDSource
	Secrets         engine.SecretSource
	Workspace       engine.Workspace
	WorkspaceRoot   string
	MaxSessions     int
	PollInterval    time.Duration
	CloseTimeout    time.Duration
	FinalizeTimeout time.Duration
}

// Spec binds serializable Run inputs to runtime adapters. Task, Target and
// Policy are used only when creating a Run; Resume uses the persisted manifest.
type Spec struct {
	Task              core.Task
	Target            core.TargetSpec
	Policy            core.RunPolicy
	Reasoner          engine.AgentSpec
	Executor          engine.AgentSpec
	ReasonStrategy    core.ReasonStrategy
	ExploreStrategy   core.ExploreStrategy
	Provisioner       engine.TargetProvisioner
	Verifier          engine.Verifier
	VerifierID        string
	VerifierDigest    string
	SecretEnv         []string
	KnownSecrets      []string
	WorkspaceRoot     string
	MaterializeSkills func(string) error
}

// Validate rejects incomplete adapter bindings before a Run is created.
func (s Spec) Validate() error {
	if strings.TrimSpace(s.Task.ID) == "" {
		return errors.New("task.id is required")
	}
	if strings.TrimSpace(s.Task.Objective) == "" {
		return errors.New("task.objective is required")
	}
	if strings.TrimSpace(s.Target.ID) == "" {
		return errors.New("target.id is required")
	}
	isolation := s.Target.Isolation
	if isolation == "" {
		isolation = core.IsolationCallerManaged
	}
	if isolation != core.IsolationCallerManaged {
		return fmt.Errorf("target.isolation %q is unsupported", isolation)
	}
	if strings.TrimSpace(s.Reasoner.ID) == "" || s.Reasoner.Runtime == nil {
		return errors.New("reasoner id and runtime are required")
	}
	if strings.TrimSpace(s.Executor.ID) == "" || s.Executor.Runtime == nil {
		return errors.New("executor id and runtime are required")
	}
	if s.Verifier == nil {
		return errors.New("verifier is required; a Run succeeds only from a verifier verdict")
	}
	if err := core.ValidateRunPolicy(core.NormalizeRunPolicy(s.Policy)); err != nil {
		return fmt.Errorf("policy: %w", err)
	}
	return nil
}

// Manifest builds the immutable description persisted at creation.
func (s Spec) Manifest(runID string, at time.Time) core.Manifest {
	policy := core.NormalizeRunPolicy(s.Policy)
	verifierID := strings.TrimSpace(s.VerifierID)
	if verifierID == "" {
		verifierID = "verifier"
	}
	return core.Manifest{
		SchemaVersion: core.ManifestSchemaVersion,
		RunID:         runID,
		Task:          s.Task,
		Target:        s.Target,
		Policy:        policy,
		Reasoner:      s.Reasoner.Descriptor(),
		Executor:      s.Executor.Descriptor(),
		Verifier:      core.VerifierDescriptor{ID: verifierID, ConfigDigest: s.VerifierDigest},
		CreatedAt:     at.UTC(),
	}
}

// ValidateRunSpec is kept as a package-level helper for callers.
func ValidateRunSpec(spec Spec) error { return spec.Validate() }
