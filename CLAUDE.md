# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repository is

Red Harness v4 is a pure Go SDK for the execution and trust boundary of one
autonomous security Run against one logical Target (`module
github.com/red-copilot/harness`, Go 1.24). It is not a generic agent framework.

- One Run, one authorized Target, one serialized Reasoner, one Executor slot.
- One writable exploration path: Claim/Intent.
- Success comes only from `Verifier.Verify` or backend terminal completion.
- SQLite schema v8 and `RunDescriptor` schema v4 are the current contract.

The HTTP/Web/TSec/sweep/hint/Bootstrap surfaces were removed. The repository is
harness-only; `pi`, `sandbox/docker`, `verify` and `store/sqlite` remain as
adapters.

## Autonomy: auto-commit and auto-push

This repository expects autonomous delivery. Once a task's required verification
passes, commit and push **without asking for confirmation**:

- Never leave verified work uncommitted or unpushed; finish the git workflow in
  the same session as the change.
- Always work on a short-lived branch off `main`; never commit directly to
  `main`, and never force-push `main`.
- Stage only the paths this task touched (`git add <path> ...`), never
  `git add -A` or `git add .`; unrelated modified or untracked files stay
  untouched.
- Push with `git push -u origin HEAD` immediately after committing, then open
  or update the PR against `main` and squash-merge it once the gates in
  `AGENTS.md` are satisfied.
- The only reasons to stop are a failed gate, an authentication/permission
  error, or a merge conflict needing explicit resolution. Report the exact
  command and error; do not work around authentication.
- Do not ask whether to commit or push — this section is the standing
  authorization. The full gate list is the "Automated Git Workflow" section in
  `AGENTS.md`.

## Architecture and dependency direction

```text
core    Pure domain contracts · validators · policy · typed events (stdlib only)
engine  Single-writer Run lifecycle · board · one slot · authority · ports
root    Thin façade: type aliases and constructors only
adapters  pi · sandbox/docker · verify · store/sqlite
internal/cli + cmd/harness  Composition root
```

Import rules are enforced by `import_boundary_test.go`:

- `core/` imports the standard library only.
- `engine/` imports only `core` and the standard library; never adapters or CLI.
- Adapters never import `internal/cli` or other adapters.
- Root `harness.go` must stay a façade; no state machine or I/O belongs there.

`engine/ports.go` defines the entire integration boundary:
`AgentRuntimeFactory`/`AgentSession`, `ProcessStarter`, `TargetProvisioner`,
`CompletionSource`/`Backend`, `Verifier`, `ArtifactStore`, `EventSink`, and the
persistence ports (`Store`, `CanonicalWriter`, `BoardReader`, `ReadStore`).
Adapters implement ports; `engine` never depends on them.

Control-loop files to know:

- `engine/run_controller.go` — single writer for phase/outcome transitions,
  budgets, sessions, authority arbitration and finalization.
- `engine/blackboard.go` — controller-owned Claim/Intent board state and the
  fixed single Executor slot.
- `engine/blackboard_engine.go` — Reason/Explore arbitration, staleness and
  stagnation handling.
- `engine/reason.go`, `engine/explore.go`, `engine/turn.go` — strategy prompts,
  report parsing and turn semantics.
- `engine/artifact.go` — evidence path validation, redaction, hashing, archive.
- `engine/runner.go` — Run composition, session slots and recovery.

`Runner` installs a `redactingStore` around the caller's `Store`, so no Run or
adapter can reach the canonical store unredacted. The CLI wires `pi` + Docker +
`store/sqlite` + `verify`.

## Run flow

```text
queued -> provisioning -> running -> finalizing -> verifying -> terminal
outcome: succeeded | failed | cancelled | timed_out | stagnated | interrupted
```

- The Reasoner reads a bounded Claim/Intent graph and proposes at most one
  Intent when the slot is free.
- Explore executes one Intent; a finding or negative claim requires a current
  Attempt Observation and archived Artifact evidence.
- Execute timeout or unusable output triggers the same-session Conclude fallback.
- A `goal_candidate` only triggers verification.
- `agent_settled` completes a turn; `agent_end` does not.
- Backend completion wins over a Verifier. Final Observe errors fail closed;
  watcher Observe errors are tolerated up to
  `max_authority_observe_failures`.
- Reasoner failures reaching `max_reasoner_failures` stop new work and enter the
  authority-terminal flow.
- A Run needs two live sessions (Reasoner + Executor); `Runner` enforces
  `MaxSessions` (default 4) at start.

## Invariants

- Model output is a proposal; code assigns IDs, states and revisions.
- Finding and negative claims require evidence; no semantic claim bypasses the
  Artifact -> Observation -> Claim chain.
- Only the engine loop writes the board.
- Canonical commits are atomic; stale revisions and malformed reports fail
  closed.
- Events and logs are append-only. Canonical events carry a SHA-256 hash chain
  (`VerifyEventChain`); SQLite triggers reject updates/deletes.
- Known secrets are redacted before persistence; binary evidence with a known
  secret is rejected.
- Target authorization identity is separate from runtime binding.
- Cleanup, persistence and authority failures map to non-success outcomes.

## Commands

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/harness

# Focused: engine loop, board revisions, budgets, runner
go test ./engine -run TestControllerIsSingleWriterUnderConcurrentCompletion
go test ./store/sqlite -run 'TestCommit|TestFinalize'
go test ./... -count=1

# Environment-dependent; never report as passing unless actually run
HARNESS_DOCKER_TESTS=1 go test ./sandbox/docker
```

CI (`.github/workflows/ci.yml`) runs exactly `go test ./...`,
`go test -race ./...`, `go vet ./...` and `go build ./cmd/harness`. Its `web`
job is known-broken because no `web/` directory is tracked; the Go `test` job is
the gate, and CI must not be edited just to turn the web job green.

CLI smoke checks (the CLI auto-loads `.env` from the working directory):

```bash
go run ./cmd/harness run --config examples/run.yaml [--db path] [--json] [--resume run-id]
go run ./cmd/harness runs list [--db path] [--limit n] [--json]
go run ./cmd/harness runs show|events|board|artifacts [--db path] --id run-id
go run ./cmd/harness runs logs [--db path] --id run-id [--tail n]
go run ./cmd/harness runs cancel [--db path] --id run-id
go run ./cmd/harness doctor [--db path]
go run ./cmd/harness backup --db path --out path [--manifest path]
go run ./cmd/harness version
git diff --check   # minimum for documentation-only changes
```

For documentation-only changes do not claim the full Go matrix was run.

## Configuration

- Configs must declare `apiVersion: harness/v4`. `internal/cli` uses strict
  `KnownFields(true)`, so unknown keys fail closed; legacy keys `workflow`,
  `pi`, `agents`, `targets`, `exploration` are rejected with a pointer to
  `examples/run.yaml`.
- Only `caller_managed` isolation is supported; target reference checks are not
  network ACLs.
- Default database: `.harness/harness.db`. `.env` and `*.ovpn` are gitignored
  and must never be committed.
- New policy knobs need defaults in `core`, a distinct CLI test proving
  independent defaults, and unknown-key rejection (see
  `internal/cli/config_v1_test.go`).

## Schemas and cutover

- SQLite schema version `8` (`store/sqlite/store.go`); `RunDescriptor` schema
  version `4` (`core.CurrentSchemaVersion`). Neither is bumped casually.
- `sqlite.Open` rejects legacy and newer schemas without mutating them.
- The approved cutover is backup-then-rebuild: create and verify a
  SHA-256/row-count manifest (`VerifyBackupManifest`), then `OpenFresh` a new
  database. `OpenFresh` refuses to overwrite an existing database or WAL/SHM
  sidecar. Never upgrade a production database in place. Rollback restores the
  verified backup; the new schema is a one-way boundary.

## Coding conventions

- Return wrapped errors with `fmt.Errorf("...: %w", err)`; no panic on
  malformed external input — fail closed with an error.
- `core` and `engine` stay deterministic and side-effect free outside declared
  ports; adapters own I/O, process and clock interaction.
- Pi RPC uses strict LF-delimited JSONL framing with a 4 MiB frame cap;
  `agent_settled` is completion, `agent_end` is not.
- Root-level `*_test.go` files are `harness_test` contract tests for package
  boundaries, engine semantics and failure paths; adapter tests live beside
  their packages. Add tests first, including negative/fail-closed cases for
  malformed input, stale revisions, evidence escape, cancellation, process
  exit, persistence failure, verification errors and cleanup.
- Use Conventional Commits with a scope, e.g. `feat(pi): ...`.

## Read before changing sensitive areas

- Package boundaries: `import_boundary_test.go`, `architecture_contract_test.go`.
- Engine semantics: `engine/` and root-level contract tests such as
  `authority_test.go`, `blackboard_engine_test.go`, `runner_concurrency_test.go`.
- Board/revision and commit semantics: `engine/blackboard.go`,
  `store/sqlite/commit.go`, `store/sqlite/store_v2_test.go`.
- Pi framing/completion: `pi/session.go` and its tests.
- SQLite schema, backup/cutover/rollback: `store/sqlite/store.go`,
  `store/sqlite/backup.go`, the `backup` CLI subcommand.
- Evidence trust: `engine/artifact.go`, `core/types_evidence.go`,
  `core/redactor.go`.
- The Go types in `core/` are the contract; any local untracked `docs/` plans
  are non-normative and must be re-checked against the code.

## Git workflow

After verification passes, complete the change autonomously — no confirmation
prompt before committing or pushing:

1. Branch off current `main` (e.g. `fix(engine): ...` → `fix/board-revision-guard`).
2. `git add` only the task's paths; commit a Conventional Commit with a scope.
3. `git push -u origin HEAD`.
4. Open or update the PR against `main` (`gh pr create` / `gh pr edit`) with the
   verification actually run and any checks deliberately skipped.
5. `gh pr merge --squash --delete-branch`, then delete the local branch and
   fast-forward `main`.

Never `git add -A`/`git add .`; never force-push `main`; never merge on
unverified or skipped checks; never edit CI to turn the known-broken `web` job
green. Leave unrelated modified or untracked files untouched. If verification
or authentication blocks the flow, stop and report the exact command and error.
See the "Automated Git Workflow" section in `AGENTS.md` for the full gates.

## References

- `README.md` (default limits, security boundary, library usage)
- `AGENTS.md` (engineering rules and full automated Git gates)
- `examples/run.yaml`, `examples/verify.sh`
- The Go types in `core/` and the contract tests at the repository root
