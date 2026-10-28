# Red Harness v2 First-Principles Architecture

- Date: 2026-10-28
- Status: Implemented
- Scope: full repository architecture

## Conclusion

Red Harness is a pure Go execution and trust boundary for one autonomous security Run against one logical Target. It lets an untrusted agent explore freely while code owns authorization identity, resource bounds, evidence provenance, audit integrity and final success.

## Irreducible Constraints

1. Model output is a proposal, not a fact.
2. Tool exit codes only describe process completion.
3. One Run has one logical Target.
4. One writer owns each mutable control state.
5. Evidence must precede Facts and Facts must precede any downstream reasoning.
6. Audit history is append-only.
7. Model contexts must have deterministic bounds.
8. Known secrets must never enter canonical persistence.
9. Failure closes the Run non-successfully.
10. Only Verifier or backend terminal completion may establish success.

## Package Architecture

- `core`: domain values, policy, validators and typed events. Standard library only.
- `engine`: Runner, Run, two-slot Fact/Intent actor, authority arbitration and ports.
- root `harness`: thin façade.
- adapters: Pi, Docker, TSec, command Verifier, SQLite.
- surfaces: CLI, HTTP/SSE, Web.

## Fact / Intent Engine

- origin and goal Facts are immutable.
- Bootstrap occupies slot-1 first.
- Exactly two Executor slots exist.
- Reasoner is serialized and fills free slots only.
- Accepted Intent create+claim is one board revision.
- Execute and Conclude are phases of one Attempt.
- Finding Fact requires Observation/Artifact evidence.
- Model context is bounded by item and byte policy.
- Reasoner failures are capped.
- Two slots refill independently.

## Persistence

`CanonicalWriter.Commit` atomically writes normalized board records, append-only Intent/Attempt versions, board revision and events. `Finalize` atomically writes terminal Run, Verdict and final event. SQLite schema v6 is additive; v5 Branch/Knowledge tables are read-only history.

## Authority

Backend terminal completion wins. Without it, Verifier runs after board quiescence. Missing authority, malformed verdict, cancellation, budget breach, cleanup failure or canonical write failure yields a non-successful terminal outcome.

## Non-goals

- No second writable exploration path.
- No resume/checkpoint replay.
- No generic Workflow engine.
- No multi-tenant/distributed worker model.
- No network ACL claim beyond caller-managed isolation.
