package core

import "errors"

// Store and replay error sentinels.
var (
	ErrStaleHead = errors.New("stale journal head")
	ErrTerminal  = errors.New("run is terminal")
	ErrNotFound  = errors.New("not found")
)
