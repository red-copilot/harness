# Red Harness · First-Principles Offensive Agent SDK

Red Harness v1 是面向安全 Agent 开发者的纯 Go SDK。它固定一条可审计的探索控制路径：一个 Run、一个逻辑 Target、代码强制的预算与信任转换，以及只能由 `Verifier` 或 backend completion 产生的权威成功结论。

> **Caller-managed isolation:** v1 只支持 `caller_managed`。SDK 校验 agent 声明的 Target 引用并记录信任假设，但不实施网络 ACL。调用方必须保证容器只能访问授权 Target 与必要控制端。

## Architecture

```text
Control plane   Run state machine · round-barrier scheduler · budgets
Execution plane AgentRuntimeFactory · Sandbox / Target Backend
Evidence plane  Observation · Artifact · Knowledge · same-Task reuse
Trust plane     Store · Redactor · Verifier · authoritative verdict
```

- 根 `harness` 包只依赖 Go 标准库。
- `pi`、`sandbox/docker`、`tsec`、`verify`、`store/sqlite`、CLI、HTTP 和 Web 都是适配器。
- 成功的唯一来源是 `Verifier` 或 `Backend.Observe` 返回的 completion。
- Agent 文本、Shell 退出码、Observation、Knowledge 和 `local_success` 都不能建立成功 verdict。
- 没有 `Workflow` 第二条控制路径，没有 checkpoint/resume，也没有 `verified` Knowledge 状态。

## Library

```go
store, err := sqlite.Open(".harness/harness.db")
if err != nil { log.Fatal(err) }
defer store.Close()

runtime := pi.NewFactory(pi.Config{Provider: "openai", Model: "gpt-5", Thinking: "high"})
runner, err := harness.NewRunner(harness.Config{
    Store: store, WorkspaceRoot: ".harness/workspaces",
})
if err != nil { log.Fatal(err) }
defer runner.Close(context.Background())

spec := harness.RunSpec{
    Task: harness.Task{ID: "demo", Objective: "Find and submit the flag."},
    Target: harness.Target{
        ID: "local", Addresses: []string{"10.0.0.5:8080"},
        Isolation: harness.IsolationCallerManaged,
    },
    Planner: harness.AgentSpec{ID: "planner", Runtime: runtime},
    Executors: []harness.AgentSpec{
        {ID: "executor-1", Runtime: runtime},
        {ID: "executor-2", Runtime: runtime},
    },
    Policy: harness.DefaultExplorationPolicy(),
    Artifacts: store,
    Verifier: verify.Command{Path: "./verify.sh"},
}
result, err := runner.Run(context.Background(), spec)
```

`RunDescriptor` 是可序列化的 CLI/HTTP/审计输入；`RunSpec` 在其上绑定 runtime、backend、verifier 和 artifact store。

## Default limits

| Policy | Default |
|---|---:|
| Tree depth | 4 |
| Tree nodes | 15 |
| Children per hypothesis branch | 2 |
| Active branches per round | 2 |
| Attempts per branch | 3 |
| Tool calls per attempt | 20 |
| Attempt wall time | 10 minutes |
| Stagnation rounds | 3 |
| Knowledge digest | 8 items / 200 bytes each / 2048 bytes total |
| Evidence | 16 files / 10 MiB each / 50 MiB total |

调度采用 round barrier：每轮最多启动 `MaxActiveBranches` 个 Attempt，全部结束后由单写者归并状态。实际并发还受 Executor 数量和 `Config.MaxSessions` 限制。

## Evidence and knowledge

- Attempt 报告中的证据路径必须是 workspace 内的普通文件。
- SDK 拒绝绝对路径、`..`、symlink escape、目录、数量/大小超限和哈希不一致。
- 文本 Artifact 在哈希前脱敏；二进制 Artifact 命中已知 secret 时拒绝归档。
- Observation 只能引用已归档 Artifact；Knowledge 只能引用当前 Attempt 的 Observation。
- Knowledge 最高信任为 `corroborated`。冲突进入 `contested` 并创建绑定两个 ID 的 review branch。
- 跨 Run 复用严格限制在同一 `Task.ID`，并受 target、scope、reuse 和过期时间过滤。
- `refuted` 与过期 Knowledge 永不注入模型上下文。

## CLI

```bash
harness doctor
harness run --config examples/run.yaml
harness serve --config examples/run.yaml --listen 127.0.0.1:8080
harness runs show --id <run-id>
harness runs events --id <run-id> --after 0
```

旧 `workflow`、`agents`、`targets`、核心 `pi` 和 `exploration.configure` 键会 fail closed，并指向 `docs/migration-v1.md`。

## Security boundary

默认 Pi + Docker 容器保留：

- 只读根文件系统
- 无 Docker socket
- `no-new-privileges`
- capability drop，按角色显式添加
- CPU、内存与 PID 限制
- 仅显式 Task/Skills/Workspace 挂载

`BENCHMARK_TOKEN` 是显式凭据例外，可进入 agent 容器；所有持久化路径必须经过 Redactor，SQLite、Artifact、Result、Verdict 与错误中不得出现明文。

## Verification

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/harness
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
```

Docker integration 与 live TSec/VPN 检查依赖运行环境，未实际运行时不得声称通过。
