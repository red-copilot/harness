package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// VerifierDescriptor identifies the verifier binding recorded in a manifest.
// The digest covers the adapter configuration that resume must re-bind.
type VerifierDescriptor struct {
	ID           string `json:"id"`
	ConfigDigest string `json:"config_digest,omitempty"`
}

// EvidenceDigest returns a stable digest of a verdict evidence payload. An
// empty payload has an empty digest.
func EvidenceDigest(evidence json.RawMessage) (string, error) {
	if len(evidence) == 0 {
		return "", nil
	}
	var value any
	if err := json.Unmarshal(evidence, &value); err != nil {
		return "", err
	}
	data, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
