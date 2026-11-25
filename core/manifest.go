package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ManifestSchemaVersion is the v5 Run manifest generation.
const ManifestSchemaVersion = 5

// Manifest is the immutable, redacted Run plan persisted at creation. Resume
// uses this, never a caller-supplied spec.
type Manifest struct {
	SchemaVersion int                `json:"schema_version"`
	RunID         string             `json:"run_id"`
	Task          Task               `json:"task"`
	Target        TargetSpec         `json:"target"`
	Policy        RunPolicy          `json:"policy"`
	Reasoner      AgentDescriptor    `json:"reasoner"`
	Executor      AgentDescriptor    `json:"executor"`
	Verifier      VerifierDescriptor `json:"verifier"`
	CreatedAt     time.Time          `json:"created_at"`
}

// Validate rejects manifests that cannot describe a v5 Run.
func (m Manifest) Validate() error {
	if m.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("unsupported manifest schema_version %d (want %d)", m.SchemaVersion, ManifestSchemaVersion)
	}
	if strings.TrimSpace(m.RunID) == "" {
		return errors.New("manifest run_id is required")
	}
	if strings.TrimSpace(m.Task.ID) == "" || strings.TrimSpace(m.Task.Objective) == "" {
		return errors.New("manifest task id and objective are required")
	}
	if strings.TrimSpace(m.Target.ID) == "" {
		return errors.New("manifest target id is required")
	}
	isolation := m.Target.Isolation
	if isolation == "" {
		isolation = IsolationCallerManaged
	}
	if isolation != IsolationCallerManaged {
		return fmt.Errorf("manifest target isolation %q is unsupported", isolation)
	}
	if strings.TrimSpace(m.Reasoner.ID) == "" {
		return errors.New("manifest reasoner id is required")
	}
	if strings.TrimSpace(m.Executor.ID) == "" {
		return errors.New("manifest executor id is required")
	}
	if strings.TrimSpace(m.Verifier.ID) == "" {
		return errors.New("manifest verifier id is required")
	}
	if m.CreatedAt.IsZero() {
		return errors.New("manifest created_at is required")
	}
	budget := m.Policy.Budget
	if budget.WallTime < 0 || budget.MaxTokens < 0 || budget.MaxModelCalls < 0 ||
		budget.MaxToolCalls < 0 || budget.MaxCostUSD < 0 {
		return errors.New("manifest budget values must not be negative")
	}
	return ValidateRunPolicy(NormalizeRunPolicy(m.Policy))
}

// Normalized fills the manifest's defaulted isolation, policy and UTC time so
// semantically equal manifests share one digest.
func (m Manifest) Normalized() Manifest {
	if m.Target.Isolation == "" {
		m.Target.Isolation = IsolationCallerManaged
	}
	m.Policy = NormalizeRunPolicy(m.Policy)
	m.CreatedAt = m.CreatedAt.UTC()
	return m
}

// CanonicalJSON returns the deterministic, normalized manifest encoding used
// for digests and persistence.
func (m Manifest) CanonicalJSON() ([]byte, error) {
	return CanonicalJSON(m.Normalized())
}

// Digest returns the SHA-256 of the canonical manifest encoding. It is the
// genesis prev_hash of the Run journal.
func (m Manifest) Digest() (string, error) {
	if err := m.Validate(); err != nil {
		return "", err
	}
	data, err := m.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
