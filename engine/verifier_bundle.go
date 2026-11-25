package engine

import (
	"context"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
)

// VerifyArtifactBytes checks streamed artifact content against its metadata.
// The workspace implementation calls it while materializing the verifier bundle.
func VerifyArtifactBytes(artifact Artifact, hasher hash.Hash, size int64) error {
	if artifact.SHA256 == "" {
		return fmt.Errorf("artifact %s has no sha256", artifact.ID)
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	if got != artifact.SHA256 {
		return fmt.Errorf("artifact %s content hash %s does not match metadata %s", artifact.ID, got, artifact.SHA256)
	}
	if artifact.Size != 0 && artifact.Size != size {
		return fmt.Errorf("artifact %s content size %d does not match metadata %d", artifact.ID, size, artifact.Size)
	}
	return nil
}

type artifactReader interface {
	ListArtifacts(context.Context, string) ([]Artifact, error)
	OpenArtifact(context.Context, string) (io.ReadCloser, error)
}

func buildEvidenceBundle(ctx context.Context, reader artifactReader, workspace Workspace, runID, root string) (string, func() error, error) {
	if workspace == nil {
		return "", nil, fmt.Errorf("workspace is required")
	}
	artifacts, err := reader.ListArtifacts(ctx, runID)
	if err != nil {
		return "", nil, fmt.Errorf("list evidence: %w", err)
	}
	return workspace.MaterializeEvidence(ctx, root, artifacts, reader.OpenArtifact)
}
