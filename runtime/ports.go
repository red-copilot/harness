// Package runtime hosts the composition root and the default implementations
// of kernel ports. The kernel itself never reads the clock, randomness or the
// process environment directly.
package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"time"
)

// SystemClock is the production Clock.
type SystemClock struct{}

// Now returns the current UTC time.
func (SystemClock) Now() time.Time { return time.Now().UTC() }

// CryptoIDs is the production IDSource.
type CryptoIDs struct{}

// NewRunID returns a 128-bit random hex Run identity.
func (CryptoIDs) NewRunID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

// OSSecrets reads secret-bearing values from the process environment. The
// composition root is the only place allowed to do this.
type OSSecrets struct{}

// LookupEnv returns the value of name and whether it is set.
func (OSSecrets) LookupEnv(name string) (string, bool) { return os.LookupEnv(name) }
