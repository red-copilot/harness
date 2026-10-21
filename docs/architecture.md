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
5. The Planner proposes bounded experiments from `Goal + Facts + Frontiers +
   recent report evidence`; it never dispatches work or establishes success.
6. Complete validated frontiers are published before dispatch. Only then are
   two Executor sessions created lazily and leased one frontier each.
7. The deterministic Controller rejects incomplete or open-ended experiments,
   deduplicates exact probe identities, validates the structured report and
   merges facts through `FactLedger`.
8. Each Executor resets Pi with `new_session`, receives one frontier and a
   bounded fact digest, executes only that probe and returns `report.json`.
   Frontier, tool and tool-call limits are enforced while execution is in
   progress. `agent_settled` ends a Pi turn; `agent_end` is only an
   intermediate observation.
9. A backend completion or a caller-provided verifier produces the only
   authoritative verdict.
10. Target, session processes and labeled Docker resources are closed in all
   outcomes.

## Exploration control

The exploration state is a run-scoped blackboard implementing the Bounded
Hypothesis Frontier Engine:

- Initialization is brief-driven: `initialized` carries the task brief and the
  deterministic seed frontiers, so the blackboard is populated before the first
  Planner call and degrades safely when planning fails.
- `Fact` records a bounded claim with evidence, targets and one of
  `observed`, `corroborated` or `refuted`.
- `FrontierItem` is one closed experiment, not a topic. It records one
  falsifiable hypothesis, one authorized target, one `probe_key`, literal
  parameters, expected evidence, failure evidence and a hard tool/time budget.
  Open, assigned, done and failed are the only lifecycle states.
- `SessionPlanner` resets its context for each plan. It owns the problem
  statement: objective, task metadata (challenge description, difficulty, flag
  counts) and authorized targets with their metadata, plus authoritative
  progress, up to eight relevant facts, recent frontier outcomes with probe
  identity and up to eight bounded report summaries. It has no tools.
- `BuildExplorerBrief` is the minimal Executor context: one objective, the one
  assigned target, one frontier contract and only the facts related to that
  target. Executors never receive the planning blackboard.
- Each of the two `SessionExplorer` instances resets its context before every
  frontier and writes one
  structured report. Malformed reports, mismatched item IDs and missing evidence
  fail closed and do not mutate the fact ledger.
- Executor sessions are started lazily by `AcquireExplorers(ctx, need)`: the
  batch size is `min(max_workers, ready frontiers)`, so a round opens exactly
  one fresh-context Executor per frontier. Unused pool slots are recorded as
  cancelled session results rather than being started.
- The Controller rejects missing probe/evidence contracts, multiple targets,
  wordlists, CIDRs, port ranges and wildcard parameters. Fingerprints are
  `target + probe_key + params`, so rephrasing a hypothesis cannot bypass
  deduplication across open and closed frontiers.
- Executors return `supported`, `refuted`, `inconclusive` or `blocked`.
  `supported` and `refuted` require at least one claim with concrete evidence.
  Only `inconclusive` work may be narrowed into a new experiment.
- The Runner records `exploration.items.planned`, `item.assigned`,
  `initialized`, `frontiers.created`, `batch.ready`, `executor.started`,
  `report.received`, `facts.merged`, `item.failed`, `round.started`,
  `round.completed` and `backend.progress` events as the audit trail.
- `facts.merged` carries the merged `Fact` values, not only their ids, so the
  console and audit trail can show the claim and its evidence.
- The console renders the blackboard as a live left-to-right node-link board
  (`start → frontier → fact`, with derived frontiers linked to their parent or
  anchor). The layout is derived from the event stream only; clicking a node
  shows its contract and the executor's live tool calls, which are attributed by
  session id (`executor-1`, `executor-2`).

Default hard limits are enforced by `SessionExplorer` rather than prompt text:

| Limit | Default |
|---|---:|
| Tool calls per Frontier | 8 |
| Wall time per Frontier | 90 seconds |
| Wall time per tool call | 30 seconds |
| Concurrent Frontiers | 3 |
| Rounds per run | 6 |
| Planner tool calls | 4 |
| Planner wall time | 120 seconds |

Crossing a limit aborts the Pi session and marks the Frontier failed. The
Controller does not retry the same fingerprint.

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
