![Red Copilot — Security Engineering for the Agentic Era](https://raw.githubusercontent.com/red-copilot/.github/main/assets/red-copilot-banner.svg)

# Red Harness · Go SDK

**A Go-native execution boundary for security agents, available as a library, CLI, and local HTTP service.**

**面向安全智能体的 Go 原生执行边界，提供库、CLI 与本地 HTTP 服务三种接入方式。** 它与 Python Runtime 相互独立，拥有自己的执行、隔离、审计和验证契约。

The agent owns its reasoning loop. The SDK governs execution, container isolation, budgets, Pi RPC transport, persistent audit records, recovery, and the hand-off to an authoritative verifier.

<sub>推理循环由智能体负责；SDK 负责执行控制、容器隔离、预算约束、Pi RPC 通信、持久化审计、恢复，以及向权威验证器交付结果。</sub>

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
   Runner (owns lifecycle,
 cancellation, audit, verdict)
          │
          ├──────────────► SQLite canonical audit
          │
          ▼
 Workflow (optional exploration)
          │
          ▼
 Isolated Agent + Target Environment
          │
          ▼
 Verifier / Benchmark Evaluator
          │
          ▼
 Authoritative Verdict
```

The core package knows only `Run`, `AgentSession`, `Store`, `Verifier`, and the
injected `Workflow` boundary. Pi, Docker, TSec, SQLite, CLI, HTTP, and Web are
adapters. Exploration is an optional package, not a second core control path.

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

spec := harness.RunSpec{
    Task: harness.Task{ID: "demo", Objective: "Find and submit the flag."},
    Target: harness.Target{ID: "local", Addresses: []string{"10.0.0.5:8080"}},
    Agents: []harness.AgentSpec{{
        ID: "pi",
        Pi: &harness.PiSpec{Provider: "openai", Model: "gpt-5", Thinking: "high"},
        EnvFrom: []string{"OPENAI_API_KEY"},
    }},
    Verifier: verify.Command{Path: "./verify.sh"},
}
if err := exploration.Configure(&spec, exploration.DefaultPolicy(), store); err != nil {
    log.Fatal(err)
}
result, err := runner.Run(ctx, spec)
```

With no workflow configured, the Runner uses the direct workflow: one isolated
session per declared agent, followed by verifier/backend authority. Add the
`github.com/red-copilot/harness/exploration` import when the binary-tree workflow
is desired.

Only the verifier or benchmark evaluator can produce an authoritative success verdict. Agent messages, tool exit codes, and self-reported completion are observations—not proof.

<sub>只有验证器或基准评测器能够给出权威的成功结论。智能体消息、工具退出码和自述完成状态都只是观测，不是证明。</sub>

### Optional Exploration Workflow · 可选探索工作流

When the exploration workflow is configured, it expands one agent spec into a
read-only Planner and fresh-context Executors outside the Harness core:

- `Branch` is the long-lived investigation unit. `Attempt` is one bounded
  execution. `Observation` is immutable evidence. `Knowledge` is a
  code-promoted, coarse-grained durable conclusion.
- Expansion is executor-driven. Only an AttemptReport with `local_success` and
  at least one Observation may propose children. `MaxChildrenPerBranch`
  defaults to 2 and applies to depth >= 1 hypothesis branches. The depth-0
  `root` is the task node, is bounded by `MaxNodes`, and is exempt from the
  child cap. The Planner only seeds root-level branches.
- Default bounds are depth 4, 15 nodes, 2 children per hypothesis branch, 2
  active branches, 3 attempts per branch, 20 tool calls and 10 minutes per
  attempt. `MaxDepth` counts `root` at depth 0, and `MaxNodes` is an
  independent tighter budget: a full binary tree through depth 4 would have
  31 nodes.
- Local branch success only controls expansion; it never establishes run
  success. Only a Verifier or backend completion can produce an authoritative
  verdict.
- Task memory is scoped by `Task.ID`. Stable knowledge can cross runs;
  instance knowledge must match the target fingerprint or be revalidated.
- Secrets, flags, and credentials are represented by references, never stored
  as knowledge values.

The CLI enables this workflow by default for single-agent YAML/JSON runs:

```yaml
exploration:
  max_depth: 4
  max_nodes: 15
  max_children_per_branch: 2
  max_active_branches: 2
  max_attempts_per_branch: 3
  max_tool_calls_per_attempt: 20
  max_wall_time_per_attempt: 10m
  stagnation_rounds: 3
```

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

The CLI loads `.env` from the current working directory before command
dispatch. Explicitly exported variables take precedence over values in the
file. Agent provider and model defaults can be supplied with `PI_PROVIDER` and
`PI_MODEL`; provider credentials use the provider-specific variable named by
the run's `env_passthrough`. Keep `.env` private with mode `600`; it is ignored
by Git.

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
- `GET /v1/runs/{id}/events` streams SSE with `after_seq` replay. When `after_seq`
  is absent, `Last-Event-ID` is used for browser reconnects.
- `GET /v1/runs/{id}/logs` returns bounded logs with `after_seq`, `limit`, and
  `tail` query parameters.
- `GET /v1/runs/{id}/verdict` returns the authoritative verdict record.
- `GET /v1/overview` returns the latest-100-run observability summary.
- `POST /v1/runs/{id}/cancel` cancels a live run.
- `POST /v1/runs/{id}/steer` sends operator guidance to a live session.
- `POST /v1/tsec/sweeps` starts a batch of TSec challenge runs.

The embedded read-only observability console is served at `/` by
`harness serve`. Its exploration view renders the v2 binary hypothesis tree:
progress, Branch depth/status, Attempt outcomes, Observations and durable
Knowledge. The view is derived from the append-only event stream and does not
allow the UI to establish run success.

## TSec · 竞赛适配

TSec uses the platform API directly from Go:

- SDK owns `list`, `start`, `close` and completion polling.
- Pi owns `submit` through the embedded `tsec-submit` skill.
- The embedded `tools` skill routes the agent to installed scanners, HTTP,
  credential, file-transfer and pivoting commands.
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

## Observability UI · 可观测界面

The React + Vite + TypeScript console lives in `web/` and is embedded into the
Go binary from `webui/dist`. It provides a read-only view of runs, live events,
raw logs, authoritative verdicts and TSec sweeps. It does not create, cancel or
steer runs.

Development uses the Vite proxy to the local API:

```bash
./harness serve --listen 127.0.0.1:8080
npm --prefix web install
npm --prefix web run dev
```

Open `http://127.0.0.1:5173`. `VITE_PROXY_TARGET` can override the proxy target.

Production builds the frontend before the Go binary:

```bash
npm --prefix web run build
go build ./cmd/harness
./harness serve
```

The UI build is intentionally not committed. `go build` and `go test` still work
without it; an unbuilt server shows build instructions instead of the SPA.

Frontend checks:

```bash
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
```

## Tests · 测试

```bash
go test ./...
go test -race ./...
go vet ./...
```

Docker and live TSec/VPN tests are environment-dependent and should be run
explicitly rather than as part of the default unit suite.
