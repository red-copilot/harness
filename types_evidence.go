package harness

import (
	"encoding/json"
	"io"
	"time"
)

// ObservationProposal references workspace files, never artifact identities.
type ObservationProposal struct {
	LocalID       string   `json:"local_id"`
	Kind          string   `json:"kind"`
	Summary       string   `json:"summary"`
	Target        string   `json:"target"`
	EvidenceFiles []string `json:"evidence_files"`
}

// Observation is immutable evidence-backed state.
type Observation struct {
	ID        string     `json:"id"`
	RunID     string     `json:"run_id"`
	BranchID  string     `json:"branch_id"`
	AttemptID string     `json:"attempt_id"`
	Kind      string     `json:"kind"`
	Summary   string     `json:"summary"`
	Target    string     `json:"target"`
	Evidence  []Artifact `json:"evidence,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// Artifact and ArtifactInput are content-addressed evidence records.
type Artifact struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id"`
	AttemptID string    `json:"attempt_id"`
	SessionID string    `json:"session_id"`
	Name      string    `json:"name"`
	MediaType string    `json:"media_type"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	Redacted  bool      `json:"redacted,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type ArtifactInput struct {
	RunID     string
	AttemptID string
	SessionID string
	Name      string
	MediaType string
	Size      int64
	Content   io.Reader
}

// Verdict is the only successful run authority.
type Verdict struct {
	Success  bool            `json:"success"`
	Score    float64         `json:"score,omitempty"`
	Message  string          `json:"message,omitempty"`
	Evidence json.RawMessage `json:"evidence,omitempty"`
}

type EvidenceArchiveRequest struct {
	RunID         string
	BranchID      string
	AttemptID     string
	SessionID     string
	WorkspaceDir  string
	Observations  []ObservationProposal
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
}
