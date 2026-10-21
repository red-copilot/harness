# Migrating to the v1 SDK

v1 is a clean API break. There are no compatibility wrappers for `Workflow`, `PiSpec`, `Task.Targets`, `exploration.Configure` or `KnowledgeVerified`.

## Run composition

| Old | v1 |
|---|---|
| `RunSpec.Agents []AgentSpec` | `RunSpec.Planner` + `RunSpec.Executors` |
| `RunSpec.Workflow` / `WorkflowRuntime` | Removed; one fixed exploration engine |
| `exploration.Configure` | `RunSpec.Policy` |
| `harness.PiSpec` | `pi.Config` / `AgentSpec.RuntimeConfig` |
| `Task.Targets []Target` | one `RunSpec.Target` |
| `AgentSession.Wait` | `agent_settled` consumed by `RunAgentTurn` |
| `KnowledgeVerified` | highest trust is `corroborated` |
| fixed security `KnowledgeKind` | adapter namespace string |
| `Observation.EvidenceRefs` | proposal `EvidenceFiles`, archived as Artifacts |

## Minimal Go mapping

```go
runtime := pi.NewFactory(pi.Config{Provider: "openai", Model: "gpt-5"})
spec := harness.RunSpec{
    Task: harness.Task{ID: "task", Objective: "authorized objective"},
    Target: harness.Target{ID: "target", Isolation: harness.IsolationCallerManaged},
    Planner: harness.AgentSpec{ID: "planner", Runtime: runtime},
    Executors: []harness.AgentSpec{{ID: "executor-1", Runtime: runtime}},
    Policy: harness.DefaultExplorationPolicy(),
    Artifacts: store,
    Verifier: verify.Command{Path: "./verify.sh"},
}
```

## CLI / HTTP

YAML/JSON describes `RunDescriptor` fields: `task`, `target`, `planner`, `executors`, `policy`, `budget`. Deployment composition injects Pi, Docker/TSec, verifier and SQLite.

These legacy keys fail closed:

- `workflow`
- `agents`
- `targets`
- top-level `pi`
- `exploration.configure`

HTTP accepts the descriptor directly or as `{"descriptor": ...}`. Function-bearing fields cannot be supplied over HTTP.

## SQLite

Opening an older database performs additive v3 migration. Existing runs, events and logs are preserved. New Branch/Attempt/Observation IDs are Run-scoped; Knowledge trust changes append versions rather than rewriting history. Reopening is idempotent.

## Secrets

- List credential variable names in `RunSpec.SecretEnv`; use `EnvFrom` on the Planner/Executor.
- `BENCHMARK_TOKEN` may enter the container but must never be persisted.
- Text evidence is redacted before hashing; binary evidence containing a known secret is rejected.
- Verification output, normalized state, errors and results pass through the same Redactor.
