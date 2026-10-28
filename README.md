# Red Harness · First-Principles Offensive Agent SDK

Red Harness v2 是面向安全 Agent 开发者的纯 Go SDK。它固定一条可审计的探索控制路径：一个 Run、一个逻辑 Target、两个 Executor 槽位、代码强制的预算与证据边界，以及只能由 `Verifier` 或 backend completion 产生的权威成功结论。

> **Caller-managed isolation:** v2 只支持 `caller_managed`。SDK 校验 agent 声明的 Target 引用并记录信任假设，但不实施网络 ACL。调用方必须保证容器只能访问授权 Target 与必要控制端。

## Architecture

```text
core    Pure domain contracts · validators · policy · typed events
engine  Single-writer Run lifecycle · Fact/Intent actor · authority arbitration
root    Thin harness façade over core/engine
adapters Pi · Docker · TSec · Verifier · SQLite
surfaces CLI · HTTP/SSE · Web
```

- `github.com/red-copilot/harness/core` 只依赖 Go 标准库。
- `github.com/red-copilot/harness/engine` 不依赖任何适配器、CLI、HTTP 或 Web。
- 根 `harness` 包只重导出常用入口，不承载状态机或 I/O。
- 成功的唯一来源是 `Verifier.Verify` 或 `CompletionSource.Observe` 的 terminal success。
- Agent 文本、Shell 退出码、Observation、Fact 和 Reason completion 都不能建立成功 verdict。
- 没有第二条可写 Branch/Knowledge 控制路径，没有 checkpoint/resume。

### Fact / Intent Engine

- 一个 Run 固定两个 Executor 槽位，每个槽位同时最多执行一个 Intent/Attempt。
- Bootstrap 先直接尝试任务，完成后由串行 Reasoner 读取有界 Fact/Intent 图并补位。
- Reasoner 只能为当前空闲槽位提议 Intent；stale board revision 或超量提议整体拒绝。
- Explore 一次只推进一个 Intent，并产出 evidence-backed Finding Fact 或 Negative Fact。
- Execute 超时或输出不可解析时，在同一存活 session 上进入 Conclude 收尾。
- Finding Fact 必须引用当前 Attempt 的 Observation/Artifact；Negative Fact 不要求 Artifact。
- `goal_candidate` 只触发 Verifier，不是成功结论。
- Reasoner 连续失败达到 `max_reasoner_failures` 后停止新增工作并进入权威终态流程。

## Library

```go
store, err := sqlite.Open(".harness/harness.db")
if err != nil { log.Fatal(err) }
defer store.Close()

runtime := pi.NewFactory(pi.Config{Provider: "openai", Model: "gpt-5", Thinking: "high"})
runner, err := harness.NewRunner(harness.Config{
    Store: store, Starter: docker.New(), WorkspaceRoot: ".harness/workspaces",
})
if err != nil { log.Fatal(err) }
defer runner.Close(context.Background())

spec := harness.RunSpec{
    Task: harness.Task{ID: "demo", Objective: "Find and submit the flag."},
    Target: harness.TargetSpec{
        ID: "local", Addresses: []string{"10.0.0.5:8080"},
        Isolation: harness.IsolationCallerManaged,
    },
    Reasoner: harness.AgentSpec{ID: "reasoner", Runtime: runtime},
    Executors: []harness.AgentSpec{
        {ID: "executor-1", Runtime: runtime},
        {ID: "executor-2", Runtime: runtime},
    },
    Policy: harness.DefaultRunPolicy(),
    Artifacts: store,
    Verifier: verify.Command{Path: "./verify.sh"},
}
result, err := runner.Run(context.Background(), spec)
```

`RunDescriptor` schema version 为 2，CLI 配置使用 `apiVersion: harness/v2`。`RunSpec` 在可序列化 descriptor 之上绑定 runtime、backend、verifier 和 artifact store。

## Default limits

| Policy | Default |
|---|---:|
| Executor slots | 2 |
| Total Intents | 20 |
| Attempts per Intent | 3 |
| Tool calls per Attempt | 20 |
| Attempt wall time | 10 minutes |
| Conclude timeout | 90 seconds |
| Stagnation rounds | 3 |
| Reasoner tool calls | 4 |
| Reasoner wall time | 2 minutes |
| Reasoner failures | 3 |
| Model board context | 64 items / 64 KiB |
| Evidence | 16 files / 10 MiB each / 50 MiB total |

两个槽位独立补位，不等待 round barrier。Reasoner 全局串行；每轮最多创建与空闲槽位数相同的 Intent。

## Evidence and audit

- Attempt 报告中的证据路径必须是 workspace 内的普通文件。
- SDK 拒绝绝对路径、`..`、symlink escape、目录、数量/大小超限和哈希不一致。
- 文本 Artifact 在哈希前脱敏；二进制 Artifact 命中已知 secret 时拒绝归档。
- Observation 只能引用已归档 Artifact；Finding Fact 只能引用当前 Attempt 的 Observation。
- SQLite 是 canonical audit store。board revision、规范化记录和对应事件通过 `Commit` 原子提交。
- Intent/Attempt 历史版本只追加；当前行只是可查询投影。
- 生产切换使用备份后重建：验证 SHA-256 与行数清单后创建全新 schema v6 数据库；旧历史不进入新可写库。

## CLI

```bash
harness doctor
harness run --config examples/run.yaml
harness serve --config examples/run.yaml --listen 127.0.0.1:8080
harness tsec sweep --config examples/tsec.yaml
harness runs show --id <run-id>
harness runs events --id <run-id> --after 0
```

旧 `workflow`、`agents`、`targets`、`planner`、核心 `pi` 和 `exploration.configure` 键会 fail closed，并指向迁移文档。

## Security boundary

默认 Pi + Docker 容器保留：

- 只读根文件系统
- 无 Docker socket
- `no-new-privileges`
- capability drop，按角色显式添加
- CPU、内存与 PID 限制
- 仅显式 Task/Skills/Workspace 挂载

`BENCHMARK_TOKEN` 是显式凭据例外，可进入 agent 容器；所有 canonical 写入统一经过 Redactor，SQLite、Artifact、Result、Verdict 与错误中不得出现明文。

HTTP 控制面面向受信操作者，默认只监听 `127.0.0.1`。若显式暴露到其他接口，操作者必须自行提供认证、授权和网络保护；服务端不会把远程请求方视为安全边界。`/v1/tsec/sweeps` 是统一 v2 Run 引擎之上的批量编排，不是第二控制写者。

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
