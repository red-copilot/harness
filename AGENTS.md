# Repository Guidelines

## Status and Direction

This is a standalone Go SDK (`github.com/red-copilot/harness`, Go 1.24) for the
Red Harness execution and trust boundary. It does not depend on Python or the
legacy `red-copilot` runtime.

Claim/Intent v4 is the current implementation (work happens on
`refactor/harness-v4`; `main` is a merge target); it uses descriptor schema v4
and SQLite schema v8. The released `main@0bbee5e` single-session exploration
path is historical context only and must not be reintroduced or extended.

The repository is deliberately harness-only: HTTP, Web, TSec, sweeps, hints and
Bootstrap have been removed. `CLAUDE.md` contains overlapping guidance; keep
both files consistent when architecture rules change.

## Layout and Boundaries

The Claim/Intent v4 architecture is defined by the Go types and the code; this
repository tracks no separate `docs/` tree.

- `core/`: pure domain contracts, policy, validators and typed events; standard
  library only.
- `engine/`: single-writer Run lifecycle, Claim/Intent board, one Executor
  slot, budgets, authority arbitration and persistence ports; imports only
  `core` and the standard library.
- Root `harness`: thin façade over common `core`/`engine` entry points.
- Adapters: `pi/` (Pi RPC session and hardened command construction),
  `sandbox/docker/` (Docker process starter, external/image/Compose target
  backends), `verify/` (external command verifier), `store/sqlite/` (canonical
  SQLite event/log/result persistence).
- `internal/naming/`: identifier rules shared by adapters that hand names to
  Docker and the Pi RPC process.
- `internal/cli/`, `cmd/harness/`: operational CLI and composition roots.
- `examples/`: run config and verifier example.

The current implementation has one writable exploration path. Every Run uses a
serialized Reasoner and exactly one Executor. Branch/Knowledge are removed and
must not regain a writable control path.

Root-level `*_test.go` files are `harness_test` contract tests covering package
boundaries, engine semantics and failure paths; adapter tests live beside their
packages.

## Toolchain and Dependencies

- Go 1.24; only `gopkg.in/yaml.v3` and `modernc.org/sqlite` (pure Go, no cgo —
  keep it that way so release binaries cross-compile).
- SQLite schema version is `8` (`store/sqlite/store.go`); `RunDescriptor`
  schema version is `4` (`core.CurrentSchemaVersion`). Neither is bumped
  casually; the cutover is backup-then-rebuild as described below.

## CLI and Configuration

```bash
go run ./cmd/harness run --config examples/run.yaml [--db path] [--json]
go run ./cmd/harness runs show|events --db path --id run-id
go run ./cmd/harness doctor [--db path]
go run ./cmd/harness backup --db path --out path [--manifest path]
go run ./cmd/harness version
```

- Configs must declare `apiVersion: harness/v4`; v1 and legacy keys are
  rejected (see `internal/cli/config_v1_test.go`).
- The CLI auto-loads a `.env` from the working directory (no `godotenv`
  dependency); `.env` and `*.ovpn` are gitignored and must never be committed.
- Default database is `.harness/harness.db`; the CLI binary built at the repo
  root (`./harness`) is gitignored.

## Build and Test

CI (`.github/workflows/ci.yml`) runs exactly:

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/harness
```

Prefer `-race` for anything touching the engine loop, board revisions,
budgets or the runner.

Focused CLI smoke checks use `go run ./cmd/harness <command>`; see
`examples/run.yaml` and `examples/verify.sh`.

Docker integration checks are environment-dependent and skipped unless the
variable is set; do not present them as passing unless they were actually run:

```bash
HARNESS_DOCKER_TESTS=1 go test ./sandbox/docker
```

Known CI gotcha: `ci.yml` also has a `web` job running `npm ci --prefix web`,
but no `web/` directory is tracked in this branch (the Web UI was removed with
the harness-only cutover). Expect that job to fail and do not describe the
workflow as green.

For documentation-only changes, at minimum run `git diff --check` and verify
links, commit references and code symbols; do not claim the full Go matrix was
run.

## Run Flow

```text
queued -> provisioning -> running -> finalizing -> verifying -> terminal
outcome: succeeded | failed | cancelled | timed_out | stagnated | interrupted
```

- The Reasoner reads a bounded Claim/Intent graph and proposes at most one
  Intent when the Executor slot is free; excess or stale-revision proposals are
  rejected wholesale.
- Explore advances one Intent/Attempt and must emit an evidence-backed finding
  claim or negative claim bound to the current Attempt's Observation and
  archived Artifact.
- Execute timeout or unparseable output triggers the same-session Conclude
  fallback (the session stays alive).
- A `goal_candidate` only triggers the Verifier; it is not a success.
- Reasoner failures reaching `max_reasoner_failures` stop new work and enter the
  authority-terminal flow.
- Backend terminal completion outranks a Verifier. Final `Observe` errors fail
  closed; watcher `Observe` errors are tolerated up to
  `max_authority_observe_failures`.

## Coding Conventions

- Return wrapped errors with `fmt.Errorf("...: %w", err)`; no panic on
  malformed external input — fail closed with an error.
- Keep package doc comments in the idiom already used (`// Package x ...`).
- Engine and core must stay deterministic and side-effect free outside declared
  ports; adapters own all I/O, process and clock interaction.
- New policy knobs need defaults in `core`, a distinct CLI test proving
  independent defaults, and rejection of unknown keys.

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
- Design history (non-normative, untracked local scratch): `docs/superpowers/`
  and `docs/reviews/` may exist in a working tree but are not tracked. The Go
  types in `core` are authoritative; do not implement a plan from those
  directories without re-checking it against current code.

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

## Automated Git Workflow

Once a task is implemented and its required verification passes, carry it
through to a merged, cleaned-up state without asking for further confirmation.
Stop and report the exact blocking reason if any gate fails.

Single remote: `origin` = `https://github.com/red-copilot/harness.git`.
`main` is the integration branch; work lands through short-lived scoped
branches such as `refactor/harness-v4` or `feat/board-revision-guard`.

1. **Stage selectively.** `git add` only the paths changed for this task. Never
   use `git add -A`/`git add .`: working trees here carry untracked noise
   (`node_modules/`, `gui-test-screenshots/`, `docs/reviews/`,
   `docs/superpowers/`, `.zcode/`, `.harness/`) that must not be swept in.
2. **Commit.** Conventional Commit with a scope (`feat(pi): ...`,
   `fix(engine): ...`, `docs(agents): ...`). One logical change per commit.
3. **Push.** Push the current branch to its upstream, setting it when absent:
   `git push -u origin HEAD`.
4. **Open or update the PR** with `gh pr create` / `gh pr view`, base `main`.
   Body states the change, the verification actually run, and any checks
   deliberately skipped.
5. **Auto-merge only when green.** History is linear (no merge commits), so
   merge with squash and delete the branch in the same step:
   `gh pr merge --squash --delete-branch`. Do not add a merge commit and do not
   rebase-merge.
6. **Delete the branch.** `--delete-branch` removes the remote branch; also
   delete the local one after switching away. Never delete `main` or a branch
   that has unmerged work.
7. **Sync afterwards.** Fetch, fast-forward `main`, and confirm the working
   tree is clean before starting the next task.

Mandatory gates — never bypass these to finish a merge:

- Required verification must have passed in this session. Do not merge on a
  guess, a partial run, or a claim that checks "should" pass.
- The `test` job in `.github/workflows/ci.yml` (Go test, `-race`, vet, build)
  must be green. The `web` job currently fails because no `web/` directory is
  tracked; treat that single job as a known-broken check, never as permission
  to ignore the other checks, and never edit CI just to turn it green.
- Never force-push (`--force`, `--force-with-lease`) to `origin/main` or to any
  branch another session may be using.
- Never merge `main` into a feature branch as a "fix" for conflicts when a
  rebase is possible; resolve conflicts explicitly and re-run verification.
- If `gh auth status` reports no logged-in host, or the push/PR/merge is
  rejected by permissions, halt at that step, leave the branch pushed, and
  report the exact command and error. Do not work around authentication.
- Leave unrelated modified or untracked files untouched; do not stash, reset,
  or clean them to make a merge easier.
