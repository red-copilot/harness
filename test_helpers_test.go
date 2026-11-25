package harness_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"

	"github.com/red-copilot/harness"
)

type fakeArtifactStore struct {
	puts  int
	fail  error
	items map[string][]byte
}

func (s *fakeArtifactStore) Put(_ context.Context, input harness.ArtifactInput) (harness.Artifact, error) {
	if s.fail != nil {
		return harness.Artifact{}, s.fail
	}
	data, err := io.ReadAll(input.Content)
	if err != nil {
		return harness.Artifact{}, err
	}
	s.puts++
	if s.items == nil {
		s.items = map[string][]byte{}
	}
	id := harness.ArtifactContentID(data)
	s.items[id] = data
	return harness.Artifact{ID: id, RunID: input.RunID, AttemptID: input.AttemptID, SessionID: input.SessionID, Name: input.Name, MediaType: input.MediaType, Size: int64(len(data)), SHA256: id, Redacted: true}, nil
}

func (s *fakeArtifactStore) Open(_ context.Context, id string) (io.ReadCloser, error) {
	data, ok := s.items[id]
	if !ok {
		return nil, errors.New("artifact not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
