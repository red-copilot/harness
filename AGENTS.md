# Repository Guidelines

## Status and Direction

This is a standalone Go SDK for the Red Harness execution and trust boundary.
It does not depend on Python or the legacy `red-copilot` runtime.

Fact/Intent v2 is the current implementation on `main`. The released
`main@0bbee5e` single-session exploration path is historical context only and
must not be reintroduced or extended.

## Current Project Structure

- `core/`: pure domain contracts, policy, validators and typed events.
- `engine/`: single-writer Run lifecycle, Fact/Intent board, two Executor slots,
  budgets, authority arbitration and persistence ports.
- Root `harness`: thin façade over common `core`/`engine` entry points.
- `pi/`: Pi RPC session implementation and hardened command construction.
- `sandbox/docker/`: Docker process starter, external/image/Compose target backends.
- `tsec/`: TSec API client, challenge backend and completion polling.
- `verify/`: external command verifier.
- `store/sqlite/`: canonical SQLite event/log/result persistence.
- `server/`: asynchronous HTTP API, bounded logs and SSE replay.
- `internal/cli/`, `cmd/harness/`: operational CLI and composition roots.
- `web/`, `webui/`: React/Vite observability console and its embedded Go server.
- `skills/tsec-submit/`: embedded Pi skill and authoritative API reference.
- `skills/tools/`: embedded security-tool selection skill with one reference
  per preinstalled command.
- `images/pi-kali/`: pinned Kali + Pi image.
- `docs/`: current architecture, Pi/TSec contracts, migration guidance and the
  first-principles architecture study.

## Current Architecture

The Fact/Intent v2 architecture is documented in
`docs/first-principles-architecture.md` and `docs/architecture.md`.

- `core/`: pure domain contracts, policy, validators and typed events; standard
  library only.
- `engine/`: single-writer Run lifecycle, Fact/Intent board, two Executor
  slots, budgets, authority arbitration and persistence ports; imports only
  `core` and the standard library.
- Root `harness`: thin façade over common `core`/`engine` entry points.
- Adapters: `pi`, `sandbox/docker`, `tsec`, `verify`, `store/sqlite`.
- Surfaces: `server`, `internal/cli`, `webui` and `web`.

The current implementation has one writable exploration path. Every Run uses a
serialized Reasoner and exactly two Executors. Branch/Knowledge are legacy
schema only and must never regain a writable control path.

## Build and Test

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/harness
```

Frontend checks:

```bash
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
```

Docker integration and live TSec/VPN checks are environment-dependent; do not
present them as passing unless they were actually run:

```bash
HARNESS_DOCKER_TESTS=1 go test ./sandbox/docker
HARNESS_TSEC_LIVE=1 BENCHMARK_BASE_URL=... BENCHMARK_TOKEN=... go test ./tsec
```

Live TSec checks require the benchmark VPN and a stopped, unfinished challenge;
they may allocate a remote challenge instance. Never print or commit the token.
For documentation-only changes, at minimum run `git diff --check` and verify
links, commit references and code symbols; do not claim the full Go/Web matrix
was run.

## Engineering Rules

- Keep the public library usable without the CLI or HTTP server.
- Never let agent messages, tool exits or benchmark prose establish success.
  Only a `Verifier` or backend terminal completion may produce a successful
  verdict. Facts, Reason completion, goal candidates and `agent_settled` are
  observations, not success.
- Preserve the v2 trust invariants:
  model output is a proposal; `Artifact -> Observation -> Fact` provenance is
  mandatory for findings; the engine loop is the only board writer; canonical
  commits are atomic; stale board revisions and malformed reports fail closed.
- Do not introduce a second writable exploration path. Do not reintroduce
  Branch/Knowledge writes. They are legacy schema compatibility only; new
  records must use Fact/Intent.
- Pi RPC uses strict LF JSONL framing and `agent_settled` for completion.
  `agent_end` is not completion.
- SQLite is the canonical audit store. Keep events/logs append-only. Board
  records, Intent/Attempt history and their events commit atomically; terminal
  Run, Verdict and `run.finalized` finalize atomically.
- The approved cutover is backup-then-rebuild: verify a SHA-256/row-count backup
  manifest before creating a fresh v2 database. Never upgrade the production
  database in place as if old Runs were v2 history. Rollback restores the
  verified backup; the new schema is a one-way boundary.
- Redact known secret values before persisting events, logs, Specs, Results,
  Verdicts and diagnostics. In v2, text evidence is redacted before hashing and
  binary evidence containing a known secret is rejected.
- Containers must not receive the Docker socket. Keep read-only root filesystem,
  `no-new-privileges`, capability dropping and resource limits.
- TSec `BENCHMARK_TOKEN` is an explicit product exception and must never appear
  in SQLite plaintext or diagnostics. It remains readable inside the agent
  container; treat that container as inside the credential trust boundary.
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
