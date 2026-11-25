package kernel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/red-copilot/harness/core"
	"github.com/red-copilot/harness/engine"
)

// ArtifactStoreError marks an evidence-store failure. It is a control error,
// not a retryable report error: the Run fails closed.
type ArtifactStoreError struct{ Err error }

func (e *ArtifactStoreError) Error() string { return "artifact store: " + e.Err.Error() }
func (e *ArtifactStoreError) Unwrap() error { return e.Err }

// archiveEvidence validates, redacts and stores every referenced file, then
// constructs Observations only after all artifact writes succeed.
func archiveEvidence(ctx context.Context, store engine.V5ArtifactWriter, redactor *core.Redactor, workspace engine.Workspace, req core.EvidenceArchiveRequest) ([]core.Observation, error) {
	if store == nil {
		return nil, errors.New("artifact store is required")
	}
	if workspace == nil {
		return nil, errors.New("workspace is required")
	}
	if len(req.Observations) == 0 {
		return nil, errors.New("attempt requires at least one observation")
	}
	if strings.TrimSpace(req.WorkspaceDir) == "" {
		return nil, errors.New("workspace root is required")
	}
	type preparedArtifact struct {
		name, mediaType string
		content         []byte
		redacted        bool
	}
	type preparedObservation struct {
		proposal  core.ObservationProposal
		artifacts []preparedArtifact
	}
	prepared := make([]preparedObservation, 0, len(req.Observations))
	totalFiles := 0
	var totalBytes int64
	for _, proposal := range req.Observations {
		if !core.AuthorizeTarget(proposal.Target, req.Target) {
			return nil, fmt.Errorf("observation target %q is not authorized", proposal.Target)
		}
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
			clean, err := cleanEvidencePath(file)
			if err != nil {
				return nil, err
			}
			readLimit := req.MaxFileBytes
			if req.MaxTotalBytes > 0 {
				remaining := req.MaxTotalBytes - totalBytes
				if remaining < 0 {
					return nil, errors.New("evidence aggregate size exceeds limit")
				}
				if readLimit <= 0 || remaining < readLimit {
					readLimit = remaining + 1
				}
			}
			opened, err := workspace.OpenEvidence(ctx, req.WorkspaceDir, clean, readLimit)
			if err != nil {
				return nil, err
			}
			content, readErr := io.ReadAll(opened.Reader)
			closeErr := opened.Reader.Close()
			if readErr != nil || closeErr != nil {
				return nil, fmt.Errorf("read evidence %q: %w", file, errors.Join(readErr, closeErr))
			}
			totalBytes += int64(len(content))
			if req.MaxFileBytes > 0 && int64(len(content)) > req.MaxFileBytes {
				return nil, fmt.Errorf("evidence file %q exceeds size limit", file)
			}
			if req.MaxTotalBytes > 0 && totalBytes > req.MaxTotalBytes {
				return nil, errors.New("evidence aggregate size exceeds limit")
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
			item.artifacts = append(item.artifacts, preparedArtifact{name: filepath.Base(file), mediaType: evidenceMediaType(content), content: content, redacted: redacted})
		}
		prepared = append(prepared, item)
	}
	now := req.Now.UTC()
	if now.IsZero() {
		return nil, errors.New("evidence archive time is required")
	}
	out := make([]core.Observation, 0, len(prepared))
	for _, item := range prepared {
		artifacts := make([]core.Artifact, 0, len(item.artifacts))
		for _, file := range item.artifacts {
			sealed, err := redactor.SealArtifact(core.ArtifactInput{
				RunID: req.RunID, AttemptID: req.AttemptID, SessionID: req.SessionID,
				Name: file.name, MediaType: file.mediaType, Size: int64(len(file.content)),
				Content: bytes.NewReader(file.content),
			})
			if err != nil {
				return nil, &ArtifactStoreError{Err: fmt.Errorf("seal artifact %q: %w", file.name, err)}
			}
			artifact, err := store.Put(ctx, sealed)
			if err != nil {
				return nil, &ArtifactStoreError{Err: fmt.Errorf("store artifact %q: %w", file.name, err)}
			}
			expectedID := artifactContentID(file.content)
			if artifact.ID == "" {
				artifact.ID = expectedID
			} else if artifact.ID != expectedID {
				return nil, &ArtifactStoreError{Err: fmt.Errorf("artifact %q identity %q does not match content %q", file.name, artifact.ID, expectedID)}
			}
			if artifact.SHA256 == "" {
				artifact.SHA256 = expectedID
			} else if artifact.SHA256 != expectedID {
				return nil, &ArtifactStoreError{Err: fmt.Errorf("artifact %q sha256 %q does not match content %q", file.name, artifact.SHA256, expectedID)}
			}
			if artifact.Size == 0 {
				artifact.Size = int64(len(file.content))
			} else if artifact.Size != int64(len(file.content)) {
				return nil, &ArtifactStoreError{Err: fmt.Errorf("artifact %q size %d does not match content %d", file.name, artifact.Size, len(file.content))}
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
		out = append(out, core.Observation{
			ID: req.AttemptID + ":obs:" + item.proposal.LocalID, RunID: req.RunID,
			IntentID: req.IntentID, AttemptID: req.AttemptID, Kind: item.proposal.Kind,
			Summary: item.proposal.Summary, Target: item.proposal.Target,
			Evidence: artifacts, CreatedAt: now,
		})
	}
	return out, nil
}

func cleanEvidencePath(candidate string) (string, error) {
	if candidate == "" || filepath.IsAbs(candidate) {
		return "", fmt.Errorf("invalid evidence path %q", candidate)
	}
	clean := filepath.Clean(candidate)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("evidence path escapes workspace: %q", candidate)
	}
	return clean, nil
}

func artifactContentID(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func evidenceMediaType(data []byte) string {
	if utf8.Valid(data) {
		return "text/plain; charset=utf-8"
	}
	return http.DetectContentType(data)
}
