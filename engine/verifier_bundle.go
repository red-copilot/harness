package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type evidenceManifest struct {
	Artifacts []Artifact `json:"artifacts"`
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
		target := filepath.Join(dir, artifact.ID[:2], artifact.ID, name)
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
		_, copyErr := io.Copy(file, reader)
		closeErr := file.Close()
		readerErr := reader.Close()
		if copyErr != nil || closeErr != nil || readerErr != nil {
			_ = os.RemoveAll(dir)
			return "", fmt.Errorf("materialize artifact %s", artifact.ID)
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
