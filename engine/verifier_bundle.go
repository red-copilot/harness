package engine

import (
	"encoding/hex"
	"fmt"
	"hash"
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
