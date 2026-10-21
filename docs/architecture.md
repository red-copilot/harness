# Architecture

Red Harness v1 is a pure Go execution and trust boundary for one autonomous security Run against one logical Target.

## Layers

1. **Control plane** — lifecycle, round-barrier exploration scheduling, depth/node/attempt/tool/wall-time budgets, cancellation and stagnation.
2. **Execution plane** — runtime-neutral `AgentRuntimeFactory`/`AgentSession`, `ProcessStarter`, Docker sandbox and Target `Backend`.
3. **Evidence plane** — immutable Observation and Artifact records, code-owned Branch/Attempt state and bounded same-Task Knowledge reuse.
4. **Trust plane** — canonical append-only Store, Redactor, Verifier and the final authoritative Result.

CLI, HTTP/SSE and Web sit outside the core. The root `harness` package imports only the standard library.

## Invariants

- A successful Result can only come from `Verifier.Verify` or a backend completion with `Done=true` and `Success=true`.
- Agent text, tool exit codes, observation content, knowledge trust and `local_success` are non-authoritative.
- Branch identity, concurrency, depth, node count, child count, attempts, tool calls and context bounds are enforced by Go.
- Evidence precedes knowledge: Knowledge references archived current-Attempt Observations; Observations reference archived Artifacts.
- Event, log, normalized exploration and knowledge history writes are append-only. Canonical write failure terminates the Run.
- Known secrets are redacted before persistence. Binary evidence containing a known secret is rejected.
- Containers never receive the Docker socket and retain read-only root, `no-new-privileges`, capability reduction and resource limits.
- There is one control path; no Workflow/direct-workflow duality and no resume.

## Lifecycle

```text
queued → provisioning → running → finalizing → verifying → terminal
```

Terminal states are `succeeded`, `failed`, `cancelled`, `timed_out`, `stagnated`, and `interrupted`. Startup marks unfinished historical Runs `interrupted`; side effects are never replayed.

## Scheduling

Each round selects ready Branches in deterministic priority/depth/ID order, starts at most `MaxActiveBranches` Attempts, waits for the entire batch, then merges in deterministic order. Shared slices and normalized state are written by the collector only.

Planner creates root-level hypotheses. Executor children require a completed `local_success` Attempt with at least one archived Observation and are capped at two per hypothesis branch. Review branches bind exactly two conflicting Knowledge IDs.

## Isolation

v1 supports `caller_managed` only. Target checks are semantic reference checks, not network ACLs. The caller owns egress rules and must ensure the agent can only reach the authorized Target and required control endpoints.

## Persistence

SQLite is canonical. Events/logs have monotonic sequences for SSE replay. Branches, attempts, observations, artifacts and knowledge versions are Run-scoped. Artifact bytes are content-addressed and stored under `.harness/artifacts/sha256/`; SQLite stores metadata and source relationships.
