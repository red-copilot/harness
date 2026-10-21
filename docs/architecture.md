# Architecture

The SDK is a Go modular monolith:

- `harness` owns run lifecycle, budgets, session fan-out, SQLite contracts and verdict handling.
- `pi` implements the default `AgentSession` using Pi RPC.
- `sandbox/docker` implements the hardened container process starter and local target backends.
- `tsec` implements the benchmark API client, challenge backend and lifecycle polling.
- `verify` implements the external command verifier.
- `server` implements the asynchronous HTTP API, bounded logs and SSE replay.
- `webui` embeds and serves the React observability SPA from the Go binary.
- `internal/cli` provides the operational commands.

## Run flow

1. The runner persists a queued run and starts one target provision operation.
2. When exploration is enabled, one ordinary `AgentSpec` expands into a
   read-only Planner and one fresh-context Executor. Custom multi-session runs
   without `Exploration` retain the direct session path.
3. The backend is observed before planning starts. Its authoritative progress,
   such as `2/6` flags, is injected into every Planner snapshot.
4. The Planner proposes bounded frontiers from `Goal + Facts + Frontiers`; it
   never dispatches work or establishes success.
5. The deterministic Controller validates authorization, deduplicates exact
   frontier fingerprints, leases one frontier to each Executor, validates the
   structured report, and merges facts through `FactLedger`.
6. Each Executor resets Pi with `new_session`, receives one frontier and a
   bounded fact digest, then returns `report.json`. `agent_settled` ends a Pi
   turn; `agent_end` is only an intermediate observation.
7. A backend completion or a caller-provided verifier produces the only
   authoritative verdict.
8. Target, session processes and labeled Docker resources are closed in all
   outcomes.

## Exploration control

The exploration state is a run-scoped blackboard:

- `Fact` records a bounded claim with evidence, targets and one of
  `observed`, `corroborated` or `refuted`.
- `FrontierItem` records one falsifiable question, authorized target, expected
  evidence and a stable fingerprint. Open, assigned, done and failed are the
  only lifecycle states.
- `SessionPlanner` resets its context for each plan and sees only the objective,
  authoritative progress, up to eight relevant facts, recent frontier outcomes
  and the authorized targets.
- `SessionExplorer` resets its context before every frontier and writes one
  structured report. Malformed reports, mismatched item IDs and missing evidence
  fail closed and do not mutate the fact ledger.
- The Controller rejects duplicate fingerprints across open and closed
  frontiers. A blocked or inconclusive result therefore cannot silently loop on
  the same action.
- The Runner records `exploration.items.planned`, `item.assigned`,
  `report.received`, `facts.merged`, `item.failed`, `round.started`,
  `round.completed` and `backend.progress` events as the audit trail.

Tool output is always an untrusted observation. Neither the Planner, Executor,
Fact ledger nor Frontier can establish task success. The legacy per-session
`ExplorationState` projection remains available for direct-session runs, but the
CLI's single-agent configuration uses the hypothesis frontier path by default.

## Trust boundary

The run root is Harness-owned. Agent workspaces, RPC messages, tool output and
target output are untrusted. They may be stored for audit but cannot establish
success. The verifier and benchmark completion are the only trusted verdict
sources.

The agent container receives only the task/skill/workspace mounts and explicit
environment variables. It never receives a Docker socket. TSec credentials are
an explicit exception chosen by the product contract; they are never written to
SQLite in plaintext.

## Persistence

The SQLite schema contains runs, sessions' events, logs, verdicts and sweeps.
Raw RPC records and stderr are append-only. SSE consumers replay by event
sequence. The service marks unfinished runs `interrupted` on startup and cleans
labeled orphan containers/networks.

There is no checkpoint/replay in v1.
