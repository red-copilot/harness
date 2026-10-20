# Architecture

The SDK is a Go modular monolith:

- `harness` owns run lifecycle, budgets, session fan-out, SQLite contracts and verdict handling.
- `pi` implements the default `AgentSession` using Pi RPC.
- `sandbox/docker` implements the hardened container process starter and local target backends.
- `tsec` implements the benchmark API client, challenge backend and lifecycle polling.
- `verify` implements the external command verifier.
- `server` implements the asynchronous HTTP API and SSE replay.
- `internal/cli` provides the operational commands.

## Run flow

1. The runner persists a queued run and starts one target provision operation.
2. Each agent gets an isolated workspace and a process-scoped `AgentSession`.
3. Pi runs as a long-lived JSONL RPC subprocess. The runner reads stdout continuously and records raw events and stderr in SQLite.
4. `agent_settled` ends a Pi run. `agent_end` is only an intermediate observation.
5. A backend completion or a caller-provided verifier produces the only authoritative verdict.
6. Target, session processes and labeled Docker resources are closed in all outcomes.

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
