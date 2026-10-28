# Migrating to the v2 Fact / Intent Engine

Red Harness v2 removes the writable Branch/Knowledge control loop and makes the two-slot Fact/Intent engine the only control path.

## Concept Mapping

| Legacy | v2 |
|---|---|
| Branch tree | Fact/Intent graph |
| Planner-created hypothesis backlog | Serialized Reasoner filling at most two free slots |
| `AttemptReport.ChildBranches` | Removed; Reasoner is the only Intent source |
| Branch `supported` | Finding Fact with Observation/Artifact evidence |
| Branch `refuted`/`blocked` | Negative Fact |
| Knowledge trust/review/reuse | Removed from new Runs; legacy records are read-only |
| Executor-only report | Explore execute + same-session Conclude fallback |

## Public API

- `RunSpec.Planner` becomes `RunSpec.Reasoner`.
- `RunSpec.Blackboard` is removed; the Fact/Intent engine is always active.
- Exactly two Executors are required.
- `RunDescriptor.SchemaVersion` is `2`; CLI configuration uses `apiVersion: harness/v2`.
- Domain contracts import from `github.com/red-copilot/harness/core`.
- Runner and adapter contracts import from `github.com/red-copilot/harness/engine`.

## Policy

Use:

- `max_open_intents` (1 or 2, default 2)
- `max_total_intents`
- `max_attempts_per_intent`
- `max_tool_calls_per_attempt`
- `max_wall_time_per_attempt`
- `conclude_timeout`
- `stagnation_rounds`
- `max_reasoner_tool_calls`
- `max_reasoner_wall_time`
- `max_reasoner_failures`
- `bootstrap_enabled`

Removed: `max_depth`, `max_nodes`, `max_children_per_branch`, `max_active_branches`, `max_attempts_per_branch`, and Knowledge policy fields.

## Persistence

Schema v6 adds append-only Intent/Attempt versions and board commits. The approved production cutover is backup-then-rebuild:

1. Create a consistent backup of the existing SQLite database.
2. Record SHA-256, size and table row counts in a manifest.
3. Verify the manifest with `sqlite.VerifyBackupManifest`.
4. Create the new database with `sqlite.OpenFresh`; never overwrite the old file in place.
5. Roll back only by restoring the verified backup.

Old Runs and Branch/Knowledge history remain in the backup. The fresh v2 database starts with zero Runs and accepts only Fact/Intent control records. Legacy table names may remain in schema v6 for compatibility, but no new Branch/Knowledge control events are written.

## Authority

Facts, Reason completion and goal candidates are never success verdicts. Only `Verifier.Verify` or `CompletionSource.Observe` terminal completion may produce a successful Run.
