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
   read-only Planner and two fresh-context Executors. Custom multi-session runs
   without `Exploration` retain the direct session path.
3. Initialization publishes the Goal, authorized targets, backend progress,
   policy and empty blackboard before any Executor process is created.
4. The backend is observed before planning starts. Its authoritative progress,
   such as `2/6` flags, is injected into every Planner snapshot.
5. The Planner proposes coarse-grained Branches from the Goal, authoritative
   progress, task memory and recent Attempt summaries; it never dispatches work
   or establishes success.
6. The deterministic tree engine validates authorization, topic+scope+kind
   deduplication, depth/node limits and active-branch limits before dispatch.
7. Each Executor resets Pi with `new_session`, receives one Branch and a
   bounded Knowledge digest, and may explore freely inside that Branch until its
   success/failure condition or budget is reached.
8. Attempts produce immutable Observations. Model-proposed Knowledge is
   validated and promoted by code. Local success may expand a Branch into two
   children; refuted/blocked Branches stop. `agent_settled` ends a Pi turn;
   `agent_end` is only an intermediate observation.
9. A backend completion or a caller-provided verifier produces the only
   authoritative verdict.
10. Target, session processes and labeled Docker resources are closed in all
   outcomes.

## Exploration control

The v2 exploration core is a binary hypothesis tree:

- `Branch` is the durable investigation unit: topic, hypothesis, scope,
  success/failure conditions, budget, status, priority and knowledge refs.
- `Attempt` is one bounded execution of a Branch. `Observation` records raw
  evidence. `Knowledge` is a code-promoted, coarse-grained conclusion.
- The Planner is read-only and proposes branches. The deterministic engine
  validates authorization, topic+scope+kind deduplication, depth, node count,
  and active-branch limits.
- A local-success branch may expand into two children. Refuted or blocked
  branches stop. Inconclusive attempts may retry within the branch budget.
  Operational failures retry once.
- Local branch success never establishes run success. Only a Backend completion
  or Verifier can produce an authoritative verdict.
- Task memory is scoped by `Task.ID`. Stable knowledge can cross runs;
  instance knowledge must match the target fingerprint or be revalidated.
  Contradictions mark both items `contested` and require a review branch.
- SQLite v2 adds normalized `exploration_branches`, `exploration_attempts`,
  `exploration_observations`, `task_knowledge` and
  `knowledge_contradictions` tables while the event stream remains the
  canonical audit source.
- On first open of a v1 database, Harness deletes exploration-run events and
  logs, preserves runs/verdicts/sweeps, creates the v2 tables, and runs
  `VACUUM`. The migration is transactional and idempotent.

Default limits:

| Limit | Default |
|---|---:|
| Tree depth | 4 |
| Tree nodes | 15 |
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
