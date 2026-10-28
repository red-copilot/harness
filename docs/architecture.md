# Architecture

## Status and direction

This file distinguishes the released baseline from the approved target architecture.

- **Released baseline:** `main@0bbee5e`. Its current executable control path is one long-lived Agent session per `AgentSpec`; Pi `turn_end` events are reduced into `ExplorationState`, projected into a bounded Frontier, and delivered with `Steer`. `agent_settled` ends the Pi session, while success still comes only from a `Verifier` or backend completion.
- **Approved target:** Fact/Intent v2, using `core` + `engine`, a serialized Reasoner, exactly two Executor slots, evidence-backed Facts, `RunPhase`/`RunOutcome`, and atomic `CanonicalWriter.Commit`/`Finalize` operations.
- **Migration status:** the target is not the current `main` implementation. The implementation evidence is the reachable local commit `5bbddb9`; migration is a separate implementation task. There is no second writable Branch/Knowledge control path in the target architecture; legacy records remain read-only.

For the derivation, route comparison, code-enforcement map, migration boundary and risks, see [`first-principles-architecture.md`](first-principles-architecture.md). The study is based on static code and test inspection; it does not claim that `main` has passed v2 or environment-dependent checks.

The released baseline is a Go modular monolith:

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
2. Each agent gets an isolated workspace and a process-scoped `AgentSession`.
3. Pi runs as a long-lived JSONL RPC subprocess. The runner reads stdout continuously and records raw events and stderr in SQLite.
4. Each Pi `turn_end` is reduced into the session's exploration state. The controller then projects a bounded frontier and steers its focus before the next model call.
5. `agent_settled` ends a Pi run. `agent_end` is only an intermediate observation.
6. A backend completion or a caller-provided verifier produces the only authoritative verdict.
7. Target, session processes and labeled Docker resources are closed in all outcomes.

## Exploration control

State and Frontier are deterministic projections over the canonical event log:

- `ExplorationState` records action attempts, bounded untrusted observations and open exploration items.
- `ReduceExploration` applies one `turn_end` event, rejects sequence regression and is idempotent for a replayed event.
- `ProjectExplorationFrontier` filters and orders open candidates/questions; it does not persist a second mutable frontier.
- The runner records `exploration.state.updated` and `exploration.frontier.updated`, then sends one bounded focus through Pi steering before the next LLM call.

Tool output is always marked as an untrusted observation. Neither State nor
Frontier can establish task success.

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

## Directional rule

The approved direction is to replace incremental Frontier steering with the Fact/Intent v2 control kernel. Do not add new exploration semantics to the released baseline: the target has one writable control path, a single board writer, evidence-backed Facts and an authoritative verdict boundary. Until the migration lands, the run flow above remains the truthful description of `main`.
