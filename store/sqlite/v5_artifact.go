package sqlite

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/red-copilot/harness/core"
	"github.com/red-copilot/harness/engine"
)

// Put stores a sealed artifact's content and returns its content-addressed
// metadata. Blob writes are idempotent by SHA-256.
func (s *V5Store) Put(ctx context.Context, sealed core.SealedArtifact) (core.Artifact, error) {
	if s.readOnly {
		return core.Artifact{}, errors.New("read-only v5 store")
	}
	input := sealed.Artifact()
	if input.Content == nil {
		return core.Artifact{}, errors.New("artifact content is required")
	}
	data, err := io.ReadAll(input.Content)
	if err != nil {
		return core.Artifact{}, fmt.Errorf("read artifact content: %w", err)
	}
	if input.Size != 0 && input.Size != int64(len(data)) {
		return core.Artifact{}, fmt.Errorf("artifact size mismatch: declared %d, actual %d", input.Size, len(data))
	}
	id := engine.ArtifactContentID(data)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO artifact_blobs(sha256,size,data,created_at) VALUES(?,?,?,?) ON CONFLICT(sha256) DO NOTHING`,
		id, len(data), data, time.Now().UnixNano()); err != nil {
		return core.Artifact{}, fmt.Errorf("store artifact blob: %w", err)
	}
	return core.Artifact{
		ID: id, RunID: input.RunID, AttemptID: input.AttemptID, SessionID: input.SessionID,
		Name: input.Name, MediaType: input.MediaType, Size: int64(len(data)), SHA256: id,
		CreatedAt: time.Now().UTC(),
	}, nil
}

// Open is the read side of the artifact composite.
func (s *V5Store) Open(ctx context.Context, sha string) (io.ReadCloser, error) {
	return s.OpenArtifact(ctx, sha)
}
