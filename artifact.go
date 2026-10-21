package harness

import (
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

// ValidateEvidencePath resolves a workspace-relative regular file and rejects
// traversal, symlink escape and directories.
func ValidateEvidencePath(root, candidate string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("workspace root is required")
	}
	if candidate == "" || filepath.IsAbs(candidate) {
		return "", fmt.Errorf("invalid evidence path %q", candidate)
	}
	clean := filepath.Clean(candidate)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("evidence path escapes workspace: %q", candidate)
	}
	evalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	resolved := filepath.Join(evalRoot, clean)
	evalPath, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve evidence path: %w", err)
	}
	rel, err := filepath.Rel(evalRoot, evalPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("evidence path escapes workspace: %q", candidate)
	}
	info, err := os.Lstat(evalPath)
	if err != nil {
		return "", fmt.Errorf("stat evidence: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("evidence path is not a regular file: %q", candidate)
	}
	return evalPath, nil
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
	totalFiles := 0
	var totalBytes int64
	type archived struct {
		proposal  ObservationProposal
		artifacts []Artifact
	}
	archivedObservations := make([]archived, 0, len(req.Observations))
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
		artifacts := make([]Artifact, 0, len(proposal.EvidenceFiles))
		for _, file := range proposal.EvidenceFiles {
			path, err := ValidateEvidencePath(req.WorkspaceDir, file)
			if err != nil {
				return nil, err
			}
			info, err := os.Lstat(path)
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
			sum := sha256.Sum256(content)
			expectedID := hex.EncodeToString(sum[:])
			artifact, err := store.Put(ctx, ArtifactInput{RunID: req.RunID, AttemptID: req.AttemptID, SessionID: req.SessionID, Name: filepath.Base(file), MediaType: mediaType(content), Size: int64(len(content)), Content: strings.NewReader(string(content))})
			if err != nil {
				return nil, fmt.Errorf("store artifact %q: %w", file, err)
			}
			if artifact.ID == "" {
				artifact.ID = expectedID
			}
			if artifact.SHA256 == "" {
				artifact.SHA256 = expectedID
			}
			if artifact.Size == 0 {
				artifact.Size = int64(len(content))
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
				artifact.Name = filepath.Base(file)
			}
			artifact.Redacted = artifact.Redacted || redacted
			if artifact.CreatedAt.IsZero() {
				artifact.CreatedAt = time.Now().UTC()
			}
			artifacts = append(artifacts, artifact)
		}
		archivedObservations = append(archivedObservations, archived{proposal: proposal, artifacts: artifacts})
	}
	now := time.Now().UTC()
	out := make([]Observation, 0, len(archivedObservations))
	for _, item := range archivedObservations {
		out = append(out, Observation{ID: req.AttemptID + ":obs:" + item.proposal.LocalID, RunID: req.RunID, BranchID: req.BranchID, AttemptID: req.AttemptID, Kind: item.proposal.Kind, Summary: item.proposal.Summary, Target: item.proposal.Target, Evidence: item.artifacts, CreatedAt: now})
	}
	return out, nil
}

func mediaType(data []byte) string {
	if utf8.Valid(data) {
		return "text/plain; charset=utf-8"
	}
	return http.DetectContentType(data)
}
