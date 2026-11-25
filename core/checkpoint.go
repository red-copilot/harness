package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// CheckpointSchemaVersion is the v5 checkpoint generation.
const CheckpointSchemaVersion = 1

// ErrCorruptJournal marks a chain, checkpoint or replay inconsistency. It is
// never repaired automatically.
var ErrCorruptJournal = errors.New("corrupt journal")

// Checkpoint is a versioned board/state snapshot anchored to a journal head.
type Checkpoint struct {
	SchemaVersion int   `json:"schema_version"`
	State         State `json:"state"`
	Board         Board `json:"board"`
}

// Validate checks the checkpoint snapshot.
func (c Checkpoint) Validate() error {
	if c.SchemaVersion != CheckpointSchemaVersion {
		return fmt.Errorf("unsupported checkpoint schema_version %d (want %d)", c.SchemaVersion, CheckpointSchemaVersion)
	}
	if err := c.State.Validate(); err != nil {
		return fmt.Errorf("checkpoint state: %w", err)
	}
	if err := c.Board.Validate(); err != nil {
		return fmt.Errorf("checkpoint board: %w", err)
	}
	if c.Board.RunID != c.State.RunID {
		return fmt.Errorf("checkpoint board run_id %q does not match state %q", c.Board.RunID, c.State.RunID)
	}
	if len(c.State.HeadHash) != 64 {
		return errors.New("checkpoint state head_hash is required")
	}
	return nil
}

// CanonicalJSON returns the deterministic checkpoint encoding.
func (c Checkpoint) CanonicalJSON() ([]byte, error) { return CanonicalJSON(c) }

// Digest returns the SHA-256 of the canonical checkpoint encoding.
func (c Checkpoint) Digest() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	data, err := c.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// Restored is the result of a validated journal replay.
type Restored struct {
	Manifest Manifest
	State    State
	Board    Board
	HeadSeq  uint64
	HeadHash string
}
