// Package core contains the pure, dependency-free domain contracts.
//
// The v5 domain additions in state.go, manifest.go, record.go, board.go,
// evidence.go and redact.go are additive. The v4 RunRecord/Event/Commit types
// remain the live engine contract until the v5 kernel cutover (P4); they are
// removed at P9. New code should prefer the v5 types.
package core
