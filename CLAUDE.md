# CLAUDE.md

Guidance for working in Red Harness.

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

## Layout

- `core/` — pure contracts, validators, policy, typed events; stdlib only.
- `engine/` — Run controller, single-writer board, one slot, authority, ports.
- root `harness` — thin façade.
- `pi/` — Pi RPC session adapter.
- `sandbox/docker/` — Docker target/provisioner adapter.
- `verify/` — external command verifier.
- `store/sqlite/` — canonical audit and board store.
- `internal/cli/`, `cmd/harness/` — `run`, `runs`, `doctor`, `backup`.

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

## Invariants

- Model output is a proposal; code assigns IDs, states and revisions.
- Finding and negative claims require evidence; no semantic claim bypasses the
  Artifact -> Observation -> Claim chain.
- Only the engine loop writes the board.
- Canonical commits are atomic; stale revisions and malformed reports fail
  closed.
- Events and logs are append-only. Canonical events carry a SHA-256 hash chain.
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
HARNESS_DOCKER_TESTS=1 go test ./sandbox/docker   # environment-dependent
```

Do not report Docker checks as passing unless actually run.

## Git workflow

After verification passes, complete the change autonomously: stage only the
task's paths, commit with a Conventional Commit scope, push (`git push -u
origin HEAD`), open or update the PR against `main`, then
`gh pr merge --squash --delete-branch`. Never `git add -A`; never force-push
`main`; never merge on unverified or skipped checks. If verification or
authentication blocks the flow, stop and report the exact error. See the
"Automated Git Workflow" section in `AGENTS.md` for the full gates.

## References

- `README.md`
- `AGENTS.md`
- `examples/run.yaml`
- The Go types in `core/` and the contract tests at the repository root
