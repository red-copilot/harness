![Red Copilot — Security Engineering for the Agentic Era](https://raw.githubusercontent.com/red-copilot/.github/main/assets/red-copilot-banner.svg)

# Red Harness · Go SDK

**A Go-native execution boundary for security agents, available as a library, CLI, and local HTTP service.**

**面向安全智能体的 Go 原生执行边界，提供库、CLI 与本地 HTTP 服务三种接入方式。** 它与 Python Runtime 相互独立，拥有自己的执行、隔离、审计和验证契约。

The agent owns its reasoning loop. The SDK provides controlled execution, container isolation, budgets, Pi RPC transport, SQLite audit records, and authoritative verification.

智能体保留自己的推理循环；SDK 提供受控执行、容器隔离、预算约束、Pi RPC 通信、SQLite 审计记录与权威验证。

> **Evidence over claims:** Agent statements and tool exit codes are observations, not proof. Only the verifier or benchmark evaluator can produce an authoritative verdict.
>
> **证据优先：** 智能体自述与工具退出码只是观测，不等于证明；权威结论由验证器或竞赛评测器给出。

<p>
  <a href="https://github.com/red-copilot/red-copilot">Python Runtime</a> ·
  <a href="https://github.com/red-copilot/harness">Go SDK</a> ·
  <a href="https://github.com/red-copilot/red-x">ARL 3.0</a>
</p>

## Architecture · 架构

```text
Go Library / CLI / HTTP
          │
          ▼
     Run Controller
          │
     ┌────┴─────┐
     ▼          ▼
Agent Runtime  SQLite Store
     │          │
     ▼          ▼
Isolated Agent  Events / Runs
     │
     ▼
Target Environment
     │
     ▼
Verifier / Benchmark Evaluator
     │
     ▼
Authoritative Verdict
```

## Requirements · 环境要求

- Go 1.24+
- Docker, with Docker Compose for Compose targets
- A Pi/Kali image, or build the bundled image:

```bash
go build ./cmd/harness
./harness image build --tag ghcr.io/red-copilot/harness-pi-kali:0.1.0
```

## Library · Go 库接入

```go
store, err := sqlite.Open(".harness/harness.db")
if err != nil {
    log.Fatal(err)
}
defer store.Close()

dockerRuntime := docker.New()
runner, err := harness.NewRunner(harness.Config{
    Store:          store,
    Starter:        dockerRuntime,
    DefaultFactory: pi.NewFactory(),
    WorkspaceRoot:  ".harness/workspaces",
    Cleaner:        dockerRuntime,
})
if err != nil {
    log.Fatal(err)
}
defer runner.Close(context.Background())

result, err := runner.Run(ctx, harness.RunSpec{
    Task: harness.Task{ID: "demo", Objective: "Find and submit the flag."},
    Target: harness.Target{ID: "local", Addresses: []string{"10.0.0.5:8080"}},
    Agents: []harness.AgentSpec{{
        ID: "pi",
        Pi: &harness.PiSpec{Provider: "openai", Model: "gpt-5", Thinking: "high"},
        EnvFrom: []string{"OPENAI_API_KEY"},
    }},
    Verifier: verify.Command{Path: "./verify.sh"},
})
```

Only the verifier or benchmark evaluator can produce a successful verdict.
Agent messages, tool exit codes and self-reported completion are observations,
not evidence.

## CLI · 命令行

Create a Go-first `harness/v1alpha1` YAML/JSON file, then run:

```bash
./harness run --file examples/run.yaml
```

The CLI also supports:

```text
harness serve
harness run --file run.yaml
harness tsec list
harness tsec run --file examples/tsec.yaml --challenge WEB-001
harness tsec run --file examples/tsec.yaml --all --parallel 3
harness runs show --id run_...
harness runs events --id run_...
harness doctor
harness migrate --task task.yaml --agent agent.yaml --out run.json
```

## HTTP API · 本地服务

```bash
export BENCHMARK_BASE_URL=...
export BENCHMARK_TOKEN=...
./harness serve --listen 127.0.0.1:8080
```

The service is intentionally local/single-user and has no authentication or
tenant isolation.
Keep the listener bound to loopback unless you configure an external security boundary. Do not expose this service directly to an untrusted network.

该服务面向本地单用户场景，不提供认证或租户隔离。除非另行配置安全边界，否则应仅监听 loopback，不要直接暴露给不可信网络。

- `POST /v1/runs` creates an asynchronous run.
- `GET /v1/runs/{id}` returns the SQLite-backed record.
- `GET /v1/runs/{id}/events` streams SSE with `after_seq` replay.
- `POST /v1/runs/{id}/cancel` cancels a live run.
- `POST /v1/runs/{id}/steer` sends operator guidance to a live session.
- `POST /v1/tsec/sweeps` starts a batch of TSec challenge runs.

## TSec · 竞赛适配

TSec uses the platform API directly from Go:

- SDK owns `list`, `start`, `close` and completion polling.
- Pi owns `submit` through the embedded `tsec-submit` skill.
- `BENCHMARK_TOKEN` is passed into the agent container for the embedded submission skill. Known token values are redacted before events and logs are written to SQLite.
- **Security boundary:** redaction protects persisted records; it does not prevent code running inside the agent container from reading the token. Treat the container as inside the credential trust boundary.
- The skill never starts or closes a challenge and is not intended to print the token.

Go 版会将 `BENCHMARK_TOKEN` 传入智能体容器供提交 skill 使用。写入 SQLite 前会对已知 Token 值脱敏，但脱敏不能阻止容器内代码读取 Token；因此容器属于凭据信任边界的一部分。

See [`docs/tsec.md`](docs/tsec.md).

## Storage and Security · 存储与安全

SQLite at `.harness/harness.db` is the canonical persistence layer. Raw RPC
events, agent stdout/stderr, session outcomes and verdicts are stored there.
Workspace files are runtime artifacts, not the authoritative record.

Containers use a read-only root filesystem, `no-new-privileges`, dropped
capabilities with `NET_RAW` by default, bounded CPU/memory/PIDs and no Docker
socket. The default HTTP listener is `127.0.0.1:8080`.

## Tests · 测试

```bash
go test ./...
go test -race ./...
go vet ./...
```

Docker and live TSec/VPN tests are environment-dependent and should be run
explicitly rather than as part of the default unit suite.
