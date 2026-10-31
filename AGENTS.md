# Repository Guidelines

## Status and Direction

This is a standalone Go SDK (`github.com/red-copilot/harness`, Go 1.24) for the
Red Harness execution and trust boundary. It does not depend on Python or the
legacy `red-copilot` runtime.

Claim/Intent v4 is the current implementation on `main`; it uses descriptor
schema v4 and SQLite schema v8. The released `main@0bbee5e` single-session
exploration path is historical context only and must not be reintroduced or
extended.

The repository is deliberately harness-only: HTTP, Web, TSec, sweeps, hints and
Bootstrap have been removed. `CLAUDE.md` contains overlapping guidance; keep
both files consistent when architecture rules change.

## Current Project Structure

- `core/`: pure domain contracts, policy, validators and typed events; standard
  library only.
- `engine/`: single-writer Run lifecycle, Claim/Intent board, one Executor slot,
  budgets, authority arbitration and persistence ports.
- Root `harness`: thin façade over common `core`/`engine` entry points.
- `pi/`: Pi RPC session implementation and hardened command construction.
- `sandbox/docker/`: Docker process starter, external/image/Compose target
  backends.
- `verify/`: external command verifier.
- `store/sqlite/`: canonical SQLite event/log/result persistence.
- `internal/cli/`, `cmd/harness/`: operational CLI and composition roots.
  Subcommands: `run`, `runs`, `doctor`, `backup`.
- `examples/`: run config and verifier example.

## Current Architecture

The Claim/Intent v4 architecture is defined by the Go types and the code; this
repository tracks no separate `docs/` tree.

- `core/`: pure domain contracts, policy, validators and typed events; standard
  library only.
- `engine/`: single-writer Run lifecycle, Claim/Intent board, one Executor
  slot, budgets, authority arbitration and persistence ports; imports only
  `core` and the standard library.
- Root `harness`: thin façade over common `core`/`engine` entry points.
- Adapters: `pi`, `sandbox/docker`, `verify`, `store/sqlite`.
- Surfaces: `internal/cli` only.

The current implementation has one writable exploration path. Every Run uses a
serialized Reasoner and exactly one Executor. Branch/Knowledge are removed and
must not regain a writable control path.

## Build and Test

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/harness
```

Focused CLI smoke checks use `go run ./cmd/harness <command>`; see
`examples/run.yaml` and `examples/verify.sh`.

Docker integration checks are environment-dependent; do not present them as
passing unless they were actually run:

```bash
HARNESS_DOCKER_TESTS=1 go test ./sandbox/docker
```

For documentation-only changes, at minimum run `git diff --check` and verify
links, commit references and code symbols; do not claim the full Go matrix was
run.

## Read Before Changing Sensitive Areas

- Package boundaries: `import_boundary_test.go`
  (`TestCoreImportsStdlibOnly`, `TestEngineDoesNotImportAdaptersOrSurfaces`,
  `TestAdaptersDoNotImportSurfaces`) and the target/evidence contract tests in
  `architecture_contract_test.go`.
- Engine semantics: the `engine/` package and the root-level contract tests.
- Pi RPC framing and completion: `pi/` (`session.go`) and its tests.
- SQLite schema, backup/cutover and rollback: `store/sqlite/store.go`,
  `store/sqlite/backup.go` and the `backup` CLI subcommand.
- Run/board contract: the Go types in `core` are the contract; the HTTP/OpenAPI
  surface has been removed.

## Engineering Rules

- Keep the public library usable without the CLI.
- Never let agent messages, tool exits or benchmark prose establish success.
  Only a `Verifier` or backend terminal completion may produce a successful
  verdict. Claims, Reason completion, goal candidates and `agent_settled` are
  observations, not success.
- Preserve the v4 trust invariants:
  model output is a proposal; `Artifact -> Observation -> Claim` provenance is
  mandatory for both finding and negative claims; the engine loop is the only
  board writer; canonical commits are atomic; stale board revisions and
  malformed reports fail closed.
- Do not introduce a second writable exploration path. Do not reintroduce
  Branch/Knowledge writes. They are removed; all new records use Claim/Intent.
- Pi RPC uses strict LF JSONL framing and `agent_settled` for completion.
  `agent_end` is not completion.
- SQLite is the canonical audit store. Keep events/logs append-only. Board
  records, Intent/Attempt history and their events commit atomically; terminal
  Run, Verdict and `run.finalized` finalize atomically.
- The approved cutover is backup-then-rebuild: verify a SHA-256/row-count backup
  manifest before creating a fresh v4 database. Never upgrade the production
  database in place as if old Runs were v4 history. Rollback restores the
  verified backup; the new schema is a one-way boundary.
- Redact known secret values before persisting events, logs, Specs, Results,
  Verdicts and diagnostics. Text evidence is redacted before hashing and
  binary evidence containing a known secret is rejected.
- Containers must not receive the Docker socket. Keep read-only root filesystem,
  `no-new-privileges`, capability dropping and resource limits.
- Backend credentials are caller-owned. A runtime may read explicitly configured
  secrets inside its container, but known secret values must never appear in
  SQLite plaintext or diagnostics.
- Keep target authorization separate from runtime binding. A provisioner may
  create addresses and container details, but must not change the authorized
  Target identity. Target reference checks are not network ACLs; callers own
  egress isolation.
- Add tests first; include negative/fail-closed cases for malformed input,
  stale revisions, evidence escape, cancellation, process exit, persistence
  failure, verification errors and cleanup.
- Use Conventional Commits with a scope, for example `feat(pi): ...`.
- After a task is implemented and its required verification passes, commit
  only the files changed for that task and push the current branch to its
  upstream automatically, without asking for additional confirmation. If
  verification fails or the push is blocked, stop and report the exact reason.
