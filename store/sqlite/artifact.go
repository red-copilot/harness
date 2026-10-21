package sqlite

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/red-copilot/harness"
)

func (s *Store) Put(ctx context.Context, input harness.ArtifactInput) (harness.Artifact, error) {
	if input.Content == nil {
		return harness.Artifact{}, errors.New("artifact content is required")
	}
	select {
	case <-ctx.Done():
		return harness.Artifact{}, ctx.Err()
	default:
	}
	limit := input.Size + 1
	if limit <= 0 {
		limit = 1
	}
	data, err := io.ReadAll(io.LimitReader(input.Content, limit))
	if err != nil {
		return harness.Artifact{}, err
	}
	if int64(len(data)) != input.Size {
		return harness.Artifact{}, fmt.Errorf("artifact size mismatch: declared %d, actual %d", input.Size, len(data))
	}
	id := harness.ArtifactContentID(data)
	if !safeArtifactID(id) {
		return harness.Artifact{}, errors.New("unsafe artifact id")
	}
	path := s.artifactPath(id)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return harness.Artifact{}, err
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return harness.Artifact{}, errors.New("artifact target is not a regular file")
		}
	} else if !os.IsNotExist(err) {
		return harness.Artifact{}, err
	} else {
		tmp, err := os.CreateTemp(filepath.Dir(path), ".artifact-*")
		if err != nil {
			return harness.Artifact{}, err
		}
		tmpName := tmp.Name()
		defer os.Remove(tmpName)
		if err := tmp.Chmod(0o600); err != nil {
			_ = tmp.Close()
			return harness.Artifact{}, err
		}
		if _, err := tmp.Write(data); err != nil {
			_ = tmp.Close()
			return harness.Artifact{}, err
		}
		if err := tmp.Sync(); err != nil {
			_ = tmp.Close()
			return harness.Artifact{}, err
		}
		if err := tmp.Close(); err != nil {
			return harness.Artifact{}, err
		}
		if err := os.Rename(tmpName, path); err != nil {
			return harness.Artifact{}, err
		}
	}
	return harness.Artifact{ID: id, RunID: input.RunID, AttemptID: input.AttemptID, SessionID: input.SessionID, Name: input.Name, MediaType: input.MediaType, Size: int64(len(data)), SHA256: id, CreatedAt: time.Now().UTC()}, nil
}

func (s *Store) Open(ctx context.Context, artifactID string) (io.ReadCloser, error) {
	if !safeArtifactID(artifactID) {
		return nil, errors.New("unsafe artifact id")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	path := s.artifactPath(artifactID)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("artifact is not a regular file")
	}
	return os.Open(path)
}

func (s *Store) artifactPath(id string) string { return filepath.Join(s.artifactRoot, id[:2], id) }
func safeArtifactID(id string) bool {
	if len(id) != 64 || strings.ContainsAny(id, `/\\..`) {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

var _ harness.ArtifactStore = (*Store)(nil)
