//go:build !unix && !windows

package lease

import "errors"

func acquire(string) (func() error, error) {
	return nil, errors.New("file leases are not supported on this platform")
}
