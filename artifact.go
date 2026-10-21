package harness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// ArtifactStoreError identifies canonical evidence-store failures, which must
// terminate the Run rather than being treated as a retryable report error.
type ArtifactStoreError struct{ Err error }

func (e *ArtifactStoreError) Error() string { return "artifact store: " + e.Err.Error() }
func (e *ArtifactStoreError) Unwrap() error { return e.Err }

// ValidateEvidencePath resolves a workspace-relative regular file and rejects
// traversal, symlink escape and directories.
func ValidateEvidencePath(root, candidate string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("workspace root is required")
	}
	evalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	path, _, err := validateEvidencePath(evalRoot, candidate)
	return path, err
}

// validateEvidencePath resolves a candidate against an already-resolved
// workspace root and returns its FileInfo to avoid a second stat.
func validateEvidencePath(evalRoot, candidate string) (string, os.FileInfo, error) {
	if candidate == "" || filepath.IsAbs(candidate) {
		return "", nil, fmt.Errorf("invalid evidence path %q", candidate)
	}
	clean := filepath.Clean(candidate)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("evidence path escapes workspace: %q", candidate)
	}
	resolved := filepath.Join(evalRoot, clean)
	evalPath, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", nil, fmt.Errorf("resolve evidence path: %w", err)
	}
	rel, err := filepath.Rel(evalRoot, evalPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("evidence path escapes workspace: %q", candidate)
	}
	info, err := os.Lstat(evalPath)
	if err != nil {
		return "", nil, fmt.Errorf("stat evidence: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("evidence path is not a regular file: %q", candidate)
	}
	return evalPath, info, nil
}

// ArchiveAttemptEvidence validates and stores every referenced file, then
// constructs Observations only after all Artifact writes succeed.
func ArchiveAttemptEvidence(ctx context.Context, store ArtifactStore, redactor Redactor, req EvidenceArchiveRequest) ([]Observation, error) {
	if store == nil {
		return nil, errors.New("artifact store is required")
	}
	if len(req.Observations) == 0 {
		return nil, errors.New("attempt requires at least one observation")
	}
	if strings.TrimSpace(req.WorkspaceDir) == "" {
		return nil, errors.New("workspace root is required")
	}
	evalRoot, err := filepath.EvalSymlinks(req.WorkspaceDir)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	type preparedArtifact struct {
		name, mediaType string
		content         []byte
		redacted        bool
	}
	type preparedObservation struct {
		proposal  ObservationProposal
		artifacts []preparedArtifact
	}
	prepared := make([]preparedObservation, 0, len(req.Observations))
	totalFiles := 0
	var totalBytes int64
	for _, proposal := range req.Observations {
		if strings.TrimSpace(proposal.LocalID) == "" {
			return nil, errors.New("observation local_id is required")
		}
		if len(proposal.EvidenceFiles) == 0 {
			return nil, fmt.Errorf("observation %q requires evidence", proposal.LocalID)
		}
		totalFiles += len(proposal.EvidenceFiles)
		if req.MaxFiles > 0 && totalFiles > req.MaxFiles {
			return nil, fmt.Errorf("evidence file limit exceeded: %d > %d", totalFiles, req.MaxFiles)
		}
		item := preparedObservation{proposal: proposal, artifacts: make([]preparedArtifact, 0, len(proposal.EvidenceFiles))}
		for _, file := range proposal.EvidenceFiles {
			path, info, err := validateEvidencePath(evalRoot, file)
			if err != nil {
				return nil, err
			}
			if req.MaxFileBytes > 0 && info.Size() > req.MaxFileBytes {
				return nil, fmt.Errorf("evidence file %q exceeds size limit", file)
			}
			totalBytes += info.Size()
			if req.MaxTotalBytes > 0 && totalBytes > req.MaxTotalBytes {
				return nil, fmt.Errorf("evidence aggregate size exceeds limit")
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read evidence %q: %w", file, err)
			}
			redacted := false
			if utf8.Valid(content) {
				cleaned := redactor.Redact(string(content))
				if cleaned != string(content) {
					redacted = true
					content = []byte(cleaned)
				}
			} else if redactor.Contains(string(content)) {
				return nil, fmt.Errorf("binary evidence %q contains a known secret", file)
			}
			item.artifacts = append(item.artifacts, preparedArtifact{name: filepath.Base(file), mediaType: mediaType(content), content: content, redacted: redacted})
		}
		prepared = append(prepared, item)
	}
	now := time.Now().UTC()
	out := make([]Observation, 0, len(prepared))
	for _, item := range prepared {
		artifacts := make([]Artifact, 0, len(item.artifacts))
		for _, file := range item.artifacts {
			artifact, err := store.Put(ctx, ArtifactInput{RunID: req.RunID, AttemptID: req.AttemptID, SessionID: req.SessionID, Name: file.name, MediaType: file.mediaType, Size: int64(len(file.content)), Content: bytes.NewReader(file.content)})
			if err != nil {
				return nil, &ArtifactStoreError{Err: fmt.Errorf("store artifact %q: %w", file.name, err)}
			}
			if artifact.ID == "" || artifact.SHA256 == "" {
				expectedID := ArtifactContentID(file.content)
				if artifact.ID == "" {
					artifact.ID = expectedID
				}
				if artifact.SHA256 == "" {
					artifact.SHA256 = expectedID
				}
			}
			if artifact.Size == 0 {
				artifact.Size = int64(len(file.content))
			}
			if artifact.RunID == "" {
				artifact.RunID = req.RunID
			}
			if artifact.AttemptID == "" {
				artifact.AttemptID = req.AttemptID
			}
			if artifact.SessionID == "" {
				artifact.SessionID = req.SessionID
			}
			if artifact.Name == "" {
				artifact.Name = file.name
			}
			artifact.Redacted = artifact.Redacted || file.redacted
			if artifact.CreatedAt.IsZero() {
				artifact.CreatedAt = now
			}
			artifacts = append(artifacts, artifact)
		}
		out = append(out, Observation{ID: req.AttemptID + ":obs:" + item.proposal.LocalID, RunID: req.RunID, BranchID: req.BranchID, AttemptID: req.AttemptID, Kind: item.proposal.Kind, Summary: item.proposal.Summary, Target: item.proposal.Target, Evidence: artifacts, CreatedAt: now})
	}
	return out, nil
}

// ArtifactContentID returns the canonical content-addressed artifact identity.
func ArtifactContentID(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func mediaType(data []byte) string {
	if utf8.Valid(data) {
		return "text/plain; charset=utf-8"
	}
	return http.DetectContentType(data)
}
