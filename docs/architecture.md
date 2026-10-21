# Architecture

Red Harness is a Go modular monolith with one minimal execution core and
optional workflows.

## Core

The root `harness` package owns only the execution and trust boundary:

- run lifecycle, cancellation and budgets;
- target provisioning through `Backend`;
- isolated agent sessions through `SessionFactory`;
- canonical event/log/run/verdict persistence through `Store`;
- redaction before persistence;
- authoritative verdict selection through `Verifier` or backend completion;
- cleanup of every opened session, process and target.

`pi`, `sandbox/docker`, `tsec`, `verify`, `store/sqlite`, `server`, `webui`, and
`internal/cli` are adapters around that core. The public library does not depend
on the CLI or HTTP server.

## Workflow boundary

`RunSpec.Workflow` is an optional injected `Workflow`. The core gives it a narrow
`WorkflowRuntime` that can:

- read the run ID, task, target, agents, budget, workspace root and latest
  backend progress;
- open unique sessions up to `Config.MaxSessions`;
- emit redacted events through the canonical store;
- redact text before normalized-state persistence.

A workflow cannot access the backend, verifier, store, or Run finalization
directly. It returns only a non-authoritative `WorkflowResult` and an error.

When `RunSpec.Workflow` is nil, the Runner uses the direct workflow: one isolated
session per declared `AgentSpec`, the standard bounded task prompt, persisted
events/logs, and normal cleanup. Only a Verifier or backend completion can make
the run successful.

## Run flow

1. The Runner persists a queued run and starts one target provision operation.
2. It prepares the Harness-owned workspace and embedded skills.
3. It constructs the workflow runtime and executes the selected workflow.
4. Sessions emit events through the runtime; rejected event/log writes stop the
   run. Known secrets and flag-like values are redacted before persistence.
5. A backend completion races the workflow. On completion the Runner records the
   authoritative verdict, cancels workflow work, and closes all sessions.
6. After workflow completion, the Runner finalizes sessions and invokes a
   caller-provided Verifier when no authoritative completion already won.
7. The Runner persists the verdict, result and terminal event, then closes the
   target backend and finishes the Run.

## Optional exploration workflow

The `exploration` package implements the binary hypothesis tree outside the
core:

- `Branch` is the durable investigation unit; `Attempt` is one bounded
  execution; `Observation` is immutable evidence; `Knowledge` is a
  code-promoted conclusion.
- The Planner is read-only and seeds root-level Branches. Executors use fresh
  Pi contexts.
- Expansion is executor-driven and code-capped. Only a `local_success` attempt
  with at least one Observation may propose children.
- The package emits `exploration.*` events and persists normalized branch,
  attempt, observation and knowledge state through the runtime-provided store.
- Local exploration success never establishes run success.

Library callers opt in with `exploration.Configure(&spec, policy, store)`. The CLI
continues to enable the default exploration policy for single-agent YAML/JSON
runs and accepts the same `exploration:` block. Server requests accept either a
top-level `exploration` object or the legacy nested `run.exploration` object.

Default exploration limits:

| Limit | Default |
|---|---:|
| Tree depth | 4 |
| Tree nodes | 15 |
| Children per hypothesis branch | 2 |
| Concurrent branches | 2 |
| Attempts per branch | 3 |
| Tool calls per attempt | 20 |
| Wall time per attempt | 10 minutes |
| Stagnation rounds | 3 |

## Trust boundary

The run root is Harness-owned. Agent workspaces, RPC messages, tool output and
target output are untrusted. They may be stored for audit but cannot establish
success. The verifier and benchmark completion are the only trusted verdict
sources.

The agent container receives only explicit task/skill/workspace mounts and
environment variables. It never receives a Docker socket. TSec credentials are
an explicit product exception inside the agent container; persisted copies are
redacted.

## Persistence

SQLite at `.harness/harness.db` is the canonical store. It contains runs,
events, raw logs, verdicts, sweeps and optional normalized exploration state.
Events and logs are append-only; schema migration must not delete historical
audit rows. SSE consumers replay by event sequence.

The service marks unfinished runs `interrupted` on startup and cleans labeled
orphan containers/networks. There is no checkpoint/replay mode.
