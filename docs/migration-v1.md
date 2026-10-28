# Migrating to the v1 SDK

v1 is a clean API break. There are no compatibility wrappers for `Workflow`, `PiSpec`, `Task.Targets`, `exploration.Configure` or `KnowledgeVerified`.

## Run composition

| Old | v1 |
|---|---|
| `RunSpec.Agents []AgentSpec` | `RunSpec.Planner` + `RunSpec.Executors` |
| `RunSpec.Workflow` / `WorkflowRuntime` | Removed; one fixed exploration engine |
| `exploration.Configure` | `RunSpec.Policy` |
| `harness.PiSpec` | `pi.Config` / `AgentSpec.RuntimeConfig` |
| `Task.Targets []Target` | one stable `RunSpec.Target` (`TargetSpec`) + per-Run `TargetBinding` |
| `AgentSession.Wait` | `agent_settled` consumed by `RunAgentTurn` |
| `KnowledgeVerified` | highest trust is `corroborated` |
| fixed security `KnowledgeKind` | adapter namespace string |
| `AttemptLocalSuccess` | Attempt phase `completed` + outcome `supported` |
| `Run.Status` as canonical state | `RunPhase` + `RunOutcome`; `status` is derived compatibility output |
| `Observation.EvidenceRefs` | proposal `EvidenceFiles`, archived as Artifacts |

## Minimal Go mapping

```go
runtime := pi.NewFactory(pi.Config{Provider: "openai", Model: "gpt-5"})
spec := harness.RunSpec{
    Task: harness.Task{ID: "task", Objective: "authorized objective"},
    Target: harness.TargetSpec{ID: "target", Isolation: harness.IsolationCallerManaged},
    Planner: harness.AgentSpec{ID: "planner", Runtime: runtime},
    Executors: []harness.AgentSpec{{ID: "executor-1", Runtime: runtime}},
    Policy: harness.DefaultRunPolicy(),
    Artifacts: store,
    Verifier: verify.Command{Path: "./verify.sh"},
}
```

## CLI / HTTP

HTTP accepts a JSON `RunDescriptor` with `task`, `target`, `planner`, `executors` and a grouped `policy` containing `budget`, `exploration`, `context`, `evidence` and `knowledge`. CLI files keep their deployment-oriented `policy` and `budget` sections and are mapped into `RunPolicy`. Deployment composition injects Pi, Docker/TSec, verifier and SQLite.

These legacy keys fail closed:

- `workflow`
- `agents`
- `targets`
- top-level `pi`
- `exploration.configure`

HTTP accepts the descriptor directly or as `{"descriptor": ...}`. Function-bearing fields cannot be supplied over HTTP. The control API is trusted-operator only and defaults to loopback.

## SQLite

`sqlite.Open` accepts only an empty or current v6 database. Legacy databases are rejected instead of upgraded in place; follow `docs/migration-v2.md` to verify a SHA-256/row-count backup and create a fresh v2 database with `sqlite.OpenFresh`. The legacy database remains available by restoring the verified backup.

## Secrets

- List credential variable names in `RunSpec.SecretEnv`; use `EnvFrom` on the Planner/Executor.
- `BENCHMARK_TOKEN` may enter the container but must never be persisted.
- Text evidence is redacted before hashing; binary evidence containing a known secret is rejected.
- Verification output, normalized state, errors and results pass through the same Redactor.
- Terminal Runs reject later Run updates, event appends and a second Finalize; Verdict is written only inside the atomic finalization transaction.
