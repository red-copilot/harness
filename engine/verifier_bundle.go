package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
)

type evidenceManifest struct {
	Artifacts []Artifact `json:"artifacts"`
}

func verifyArtifactBytes(artifact Artifact, hasher hash.Hash, size int64) error {
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

func buildEvidenceBundle(ctx context.Context, store Store, runID, root string) (string, error) {
	artifacts, err := store.ListArtifacts(ctx, runID)
	if err != nil {
		return "", fmt.Errorf("list evidence: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(root, "verifier-")
	if err != nil {
		return "", err
	}
	for _, artifact := range artifacts {
		reader, err := store.OpenArtifact(ctx, artifact.ID)
		if err != nil {
			_ = os.RemoveAll(dir)
			return "", fmt.Errorf("open artifact %s: %w", artifact.ID, err)
		}
		name := filepath.Base(artifact.Name)
		if name == "." || name == string(filepath.Separator) {
			name = artifact.ID
		}
		target := filepath.Join(dir, artifact.ID[:2], artifact.ID, artifact.AttemptID, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			_ = reader.Close()
			_ = os.RemoveAll(dir)
			return "", err
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = reader.Close()
			_ = os.RemoveAll(dir)
			return "", err
		}
		hasher := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(file, hasher), reader)
		closeErr := file.Close()
		readerErr := reader.Close()
		if copyErr != nil || closeErr != nil || readerErr != nil {
			_ = os.RemoveAll(dir)
			return "", fmt.Errorf("materialize artifact %s", artifact.ID)
		}
		if err := verifyArtifactBytes(artifact, hasher, written); err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
	}
	data, err := json.MarshalIndent(evidenceManifest{Artifacts: artifacts}, "", "  ")
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}
