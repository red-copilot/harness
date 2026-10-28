# CLAUDE.md

Guidance for working in Red Harness.

## What this repository is

Red Harness v2 is a pure Go SDK for the execution and trust boundary of one autonomous security Run against one logical Target (`module github.com/red-copilot/harness`, Go 1.24).

The package boundary is part of the architecture:

- `core/` — pure domain contracts and validators; standard library only.
- `engine/` — Runner, Run lifecycle, two-slot Fact/Intent actor, authority arbitration and persistence ports; imports only `core` and the standard library.
- root `harness` — thin façade over common engine/core entry points.
- adapters — `pi`, `sandbox/docker`, `tsec`, `verify`, `store/sqlite`.
- surfaces — `server`, `internal/cli`, `webui`, `web`.

`import_boundary_test.go` and `core/import_boundary` tests enforce these boundaries. Breaking them is an architecture regression.

## Non-negotiable invariants

- Only `Verifier.Verify` or terminal `CompletionSource.Observe` can establish success.
- Agent text, tool exits, Facts, Observations, Reason completion and goal candidates never establish success.
- A Run has exactly two Executors and at most two active Intent/Attempt slots.
- Bootstrap runs first; the Reasoner is serialized and may create only one Intent per free slot.
- Stale board revisions, over-capacity proposals and malformed structured output fail closed.
- Finding Fact requires a current-Attempt Observation; Observation requires archived Artifact bytes.
- Canonical write failure, cleanup failure, cancellation and budget exhaustion terminate non-successfully.
- SQLite is canonical. Board records and events commit atomically; Intent/Attempt history is append-only.
- Known secrets are redacted before all persistence. Binary evidence containing a known secret is rejected. `BENCHMARK_TOKEN` may enter the agent container but never SQLite plaintext or diagnostics.
- Containers never receive the Docker socket and remain read-only-root, `no-new-privileges`, cap-dropped and resource-limited.

## Commands

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/harness
```

```bash
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
```

Environment-gated checks:

```bash
HARNESS_DOCKER_TESTS=1 go test ./sandbox/docker
HARNESS_TSEC_LIVE=1 BENCHMARK_BASE_URL=... BENCHMARK_TOKEN=... go test ./tsec
```

Do not report Docker or live TSec/VPN checks as passing unless actually run.

## Run flow

1. `engine.ValidateRunSpec` requires task, logical Target, caller-managed isolation, one Reasoner runtime, exactly two Executor runtimes, artifact store and authority.
2. Runner persists a queued Run, provisions the Target and starts the two-slot Fact/Intent engine.
3. Bootstrap directly attempts the objective. Once it settles, the serialized Reasoner fills free slots from a bounded Fact/Intent/Hint snapshot.
4. Executor workers only consume turns and return immutable attempt results. The engine loop owns all board mutations.
5. Explore produces a Finding or Negative Fact. Execute timeouts/parse failures use same-session Conclude before retry/abandon.
6. After board quiescence, backend terminal completion wins; otherwise Verifier runs. The terminal Run, Verdict, board snapshot and `run.finalized` commit atomically.

Pi and Executor share `engine.AgentSession`. Pi framing is strict LF JSONL and only `agent_settled` completes a turn; `agent_end` is audit-only. `RunAgentTurn` enforces tool calls, assistant bytes and tool timeouts.

## Package map

- `core/types_*.go` — Task/Target, policy, run lifecycle, Fact/Intent/Attempt, evidence and typed event contracts.
- `engine/ports.go` — runtime, target, authority, canonical writer and read-store ports. `Commit` is the multi-record write boundary; `Finalize` is the terminal transaction.
- `engine/blackboard.go` — pure board reducer and two-slot admission rules.
- `engine/blackboard_engine.go` — serialized actor loop, Reasoner/Explore workers, evidence collection and slot refill.
- `engine/run_controller.go` — Run lifecycle, event publication, authority watcher and terminal finalization.
- `engine/artifact.go` — evidence path checks and content-addressed archival.
- `engine/redacting_store.go` — Run-scoped canonical write boundary.
- `pi/` — hardened Pi RPC adapter.
- `sandbox/docker/` — Docker process/backend and sandbox hardening.
- `tsec/` — TSec provisioner and completion authority.
- `verify/` — external command Verifier.
- `store/sqlite/` — schema v6 canonical store with atomic board commits and legacy read tables.
- `server/` — trusted-operator HTTP API and SSE replay. Graph endpoint is `/v1/runs/{id}/graph`.
- `internal/cli/`, `cmd/harness/` — `harness/v2` composition root. Requires exactly two Executors.
- `webui/` and `web/` — embedded read-only console. New Runs render the Fact/Intent graph; legacy branch data is read-only.
- `skills/` — embedded TSec submission and security-tool selection skills.

## Working conventions

- Add tests first, especially malformed structured output, cancellation, process exit, stale revision, persistence failure, evidence escape/secret leakage and cleanup.
- Never add a second writable exploration path or reintroduce Knowledge/Branch writes for new Runs.
- Use Conventional Commits with a scope.
- Per `AGENTS.md`, commit only changed task files and push the current branch after required verification passes. If verification or push fails, stop and report the exact reason.
- More detail: `README.md`, `docs/architecture.md`, `docs/pi.md`, `docs/tsec.md`, `docs/migration-v2.md`.
