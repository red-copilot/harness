package sqlite

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/red-copilot/harness/core"
	"github.com/red-copilot/harness/engine"
)

func (s *Store) Put(ctx context.Context, input core.ArtifactInput) (core.Artifact, error) {
	if input.Content == nil {
		return core.Artifact{}, errors.New("artifact content is required")
	}
	select {
	case <-ctx.Done():
		return core.Artifact{}, ctx.Err()
	default:
	}
	limit := input.Size + 1
	if limit <= 0 {
		limit = 1
	}
	data, err := io.ReadAll(io.LimitReader(input.Content, limit))
	if err != nil {
		return core.Artifact{}, err
	}
	if int64(len(data)) != input.Size {
		return core.Artifact{}, fmt.Errorf("artifact size mismatch: declared %d, actual %d", input.Size, len(data))
	}
	id := engine.ArtifactContentID(data)
	if !safeArtifactID(id) {
		return core.Artifact{}, errors.New("unsafe artifact id")
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO artifact_blobs(sha256,size,data) VALUES(?,?,?) ON CONFLICT(sha256) DO NOTHING`, id, len(data), data); err != nil {
		return core.Artifact{}, fmt.Errorf("store artifact blob: %w", err)
	}
	return core.Artifact{ID: id, RunID: input.RunID, AttemptID: input.AttemptID, SessionID: input.SessionID, Name: input.Name, MediaType: input.MediaType, Size: int64(len(data)), SHA256: id, CreatedAt: time.Now().UTC()}, nil
}

func (s *Store) OpenArtifact(ctx context.Context, artifactID string) (io.ReadCloser, error) {
	return s.Open(ctx, artifactID)
}

func (s *Store) Open(ctx context.Context, artifactID string) (io.ReadCloser, error) {
	if !safeArtifactID(artifactID) {
		return nil, errors.New("unsafe artifact id")
	}
	var data []byte
	if err := s.db.QueryRowContext(ctx, `SELECT data FROM artifact_blobs WHERE sha256=?`, artifactID).Scan(&data); err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func safeArtifactID(id string) bool {
	if len(id) != 64 || strings.ContainsAny(id, `/\\..`) {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

var _ engine.ArtifactStore = (*Store)(nil)
