# Architecture

Red Harness v3 is a pure Go execution and trust boundary for one autonomous security Run against one logical Target. It is not a generic agent framework: one writable control path, explicit authority, bounded exploration and an append-only audit.

## Package Layers

1. `core/` — pure domain contracts, validators, policy and typed events; standard library only.
2. `engine/` — serialized Run lifecycle, one Reasoner, one Executor, Claim/Intent state machine, budgets and ports; imports only `core` and the standard library.
3. root `harness` — a thin façade over common engine/core entry points.
4. adapters — `pi`, `sandbox/docker`, `tsec`, `verify`, `store/sqlite` implement engine ports.
5. surfaces — `server`, `internal/cli`, `webui` and `web` consume the canonical read projections.

## Run Lifecycle

```text
queued -> provisioning -> running -> finalizing -> verifying -> terminal
outcome: succeeded | failed | cancelled | timed_out | stagnated | interrupted
```

`RunPhase` and `RunOutcome` are canonical; legacy run status projections are removed. On startup, unfinished Runs are atomically marked interrupted and their side effects are not replayed.

## Serial Execution

Each Run has one serialized Reasoner and one Executor. At most one Attempt is active.

```text
Bootstrap -> Reason -> Claim Intent -> Execute/Conclude -> Evidence
          -> Claim/Attempt commit -> Verify -> Finalize
```

Structured Agent output is read only from the bounded `/workspace/report.json` file after `agent_settled`. Assistant prose is audit-only. Execute and Conclude are phases of the same Attempt; `agent_end` is not completion.

The Context Builder bounds Claim/Hint/Report inputs by deterministic item and byte limits. Stale board revisions and malformed reports fail closed.

## Claims and Evidence

- `origin` and `goal` are immutable anchors.
- A `Claim` is an admitted, provenance-linked declaration; it is not objective truth or success.
- A Finding Claim requires an Observation from the current Attempt.
- An Observation requires at least one Artifact.
- Artifact bytes are redacted before hashing and stored in SQLite with Observation/Claim/Attempt records.
- Negative Claims record ruled-out or no-progress directions and never establish success.
- Model output cannot create IDs, anchors, Claims or Verdicts directly.

## Authority

- Backend terminal completion is authoritative.
- Without backend completion, the Verifier runs after the board is quiescent using a temporary read-only evidence bundle.
- Backend completion wins over a concurrent Verifier result.
- Claim, Reason completion, verification candidates, Agent text and tool exits never establish success.
- Cleanup failure downgrades a successful candidate to failed.

## Persistence

SQLite schema v7 is canonical and self-contained: artifact bytes, metadata, board projections and events live in one database. Every board transition commits with an expected revision and atomic events. Terminal Run, Verdict and `run.finalized` commit together.

Events and logs are append-only. Canonical events carry a SHA-256 hash chain; `harness doctor` verifies it. Backups use one consistent SQLite snapshot with hash, row-count, artifact-byte and event-head checks.

## HTTP and Web

HTTP is a trusted-operator surface and defaults to loopback. OpenAPI 3.0.3 at `api/openapi.yaml` is the contract source for generated Go and TypeScript types.

`GET /v1/runs/{id}/board` returns Claims, Intents, Hints, Attempts, Observations, Artifact metadata, one Executor lane, board revision and authority activity. SSE replays canonical events from `seq`; artifact bytes are available through the canonical download endpoint. The Web console renders a single-lane evidence chain.

## Isolation

Only `caller_managed` isolation is supported. `TargetSpec` is stable authorization identity; `TargetBinding` holds per-Run runtime addresses. The SDK performs semantic reference checks, not network ACLs. Callers own egress rules.

Containers never receive the Docker socket and retain read-only root, `no-new-privileges`, dropped capabilities and CPU/memory/PID limits.
