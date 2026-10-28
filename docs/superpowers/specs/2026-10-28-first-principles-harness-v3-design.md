# Red Harness v3 First-Principles Design

- Date: 2026-10-28
- Status: Implemented
- Scope: full repository architecture

## Conclusion

Red Harness v3 is a pure Go execution and trust boundary for one autonomous security Run against one logical Target. It uses one serialized Reasoner, one Executor, one active Attempt and one canonical control writer.

## Irreducible Constraints

1. Model output is an untrusted proposal.
2. Structured Agent output is read only from the bounded `report.json` file after `agent_settled`.
3. One Run has one authorized Target and one Executor.
4. Only Verifier or backend terminal completion can establish success.
5. Evidence precedes Claims; Claims never establish success.
6. The engine loop is the only board writer.
7. Board, evidence and canonical events commit atomically.
8. Canonical events are append-only and hash-chained.
9. Context, evidence and execution budgets are mechanically bounded.
10. Crash recovery marks unfinished Runs interrupted and never replays side effects.

## Public Model

- `Claim` replaces the former `Fact` name.
- `origin` and `goal` remain immutable anchors.
- `Intent` references origin or existing Claim IDs.
- `AttemptReport` is strict JSON from `report.json`.
- `ReasonReport` contains at most one Intent or one verification candidate.
- Run records expose phase and outcome, not the legacy status projection.
- `AgentSession` has no live steer operation; operator input enters through bounded audited Hints.

## Execution

```text
provision -> bootstrap -> reason -> claim intent -> execute/conclude
          -> archive evidence -> commit Claim/Attempt -> verify -> finalize
```

The Reasoner and Executor are independent long-lived sessions, reset before each turn. A backend watcher may observe authority concurrently, but every completion is serialized through the engine loop.

## Persistence

SQLite schema v7 is canonical and self-contained. Artifact bytes and metadata live in the same database. Every control transition uses an expected board revision and commits records plus events atomically. Canonical events form a SHA-256 hash chain verified by the store. Terminal Run, Verdict and `run.finalized` commit together.

## Surfaces

OpenAPI 3.0.3 is the transport contract source. Go and TypeScript bindings are generated and committed. The read surface exposes `/v1/runs/{id}/board`, replayable SSE, artifact download and authoritative Verdict. The Web console renders one Executor lane and the Claim/Observation/Artifact chain.

## Non-goals

No checkpoint resume, distributed workers, generic workflow engine, multi-target Run, cross-Run Knowledge or network ACL claim.
