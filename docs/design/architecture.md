# v5 目标架构

> **状态：v5 目标设计（提案）。** 当前代码仍是 v4 / schema 8。实现进度见 [migration.md](migration.md)。

## 1. 系统边界

Red Harness 是纯 Go SDK，不是通用 Agent 框架，也不是托管服务。它执行**一个** Run，针对**一个**逻辑 Target，使用**一个**串行 Reasoner 与**一个** Executor 槽位，最终由外部 Verifier 给出权威结论。

v5 明确不做的事：

- 不实施网络 ACL。`caller_managed` 仍然是唯一隔离模式；SDK 校验 Target 身份与证据引用，不保证容器只能访问授权目标。
- 不把模型文本、Shell 退出码、Observation、Claim、usage 或 Reason completion 当作成功。
- 不提供第二个可写探索路径。
- 不在 kernel 中启动进程、读写环境变量、访问文件系统或读取时钟。
- 不维护两个运行时。过渡期的 v4 存储桥在 P9 删除。

## 2. 第一性原理推导

推导顺序固定为：不可约目标 → 必须永远为真的性质 → 能保证这些性质的最小组件 → 其余一律视为可替换实现。

1. **不可约目标**：对一个已授权目标执行一次可审计的探索，并给出可信的成功/失败结论。
2. **必须为真的性质**：见 [invariants.md](invariants.md)。核心是"成功只来自外部权威"、"证据链不可伪造"、"历史单一写者且可回放"、"任何失败都非成功"。
3. **最小组件**：
   - 纯领域模型（类型、校验、状态机、board、记录、策略）。
   - 单 Run kernel（驱动器、仲裁、预算、恢复规划）。
   - 窄端口（journal、reader、recovery、artifact、session、target、verifier、clock、IDs、workspace、secrets）。
   - 适配器（Pi、Docker、命令验证器、SQLite）。
   - 组合根（配置、注册表、lease、信号）。
   - 操作面（CLI）。
4. **被拒绝的偶然实现**：双权威、`finalizing` 阶段、21 方法 `Store`、实体表作为真相、无版本快照、全局链、调用方 spec 恢复、冒号 ID 序号、双份 usage、根 facade/别名层、kernel 内的进程启动与孤儿清理、`RuntimeConfig any`。

## 3. 目标包布局

```text
github.com/red-copilot/harness
├── core/                纯领域，仅标准库
│   ├── state.go         Phase/Outcome/State/Transition/Advance
│   ├── manifest.go      Manifest、规范 JSON、digest
│   ├── board.go         Claim/Intent/Attempt/Observation/Board/Apply
│   ├── evidence.go      Artifact/Verdict/证据规则
│   ├── record.go        Record、类型注册表、链哈希、解码
│   ├── redact.go        Redactor、SealedBatch/SealedArtifact/SealedLog
│   ├── policy.go        RunPolicy 与默认值/校验
│   └── target.go        TargetSpec/TargetBinding/授权
├── engine/              kernel + ports，仅 core + 标准库
│   ├── ports.go         Journal/Reader/Recovery/ArtifactWriter/...
│   ├── runner.go        New/Start/Resume/Close、活跃 Run 表
│   ├── controller.go    单写者状态转移、finalize、panic 收敛
│   ├── board.go         board actor、单槽、停滞
│   ├── arbitration.go   仅 verifier 的仲裁
│   ├── recovery.go      回放校验、恢复计划、中断
│   ├── evidence.go      证据归档、verifier bundle
│   ├── reason.go        默认 Reason 提示词/解析
│   ├── explore.go       默认 Explore + 同 session Conclude
│   ├── turn.go          单次 agent turn
│   └── porttest/        端口一致性测试套件
├── pi/                  AgentRuntimeFactory/AgentSession 适配器（保留路径）
├── sandbox/docker/      TargetProvisioner、进程启动/清理（保留路径）
├── verify/              Verifier 适配器（保留路径）
├── store/sqlite/        v5 journal/read model/artifact/lease/cutover（保留路径）
├── runtime/             唯一组合根：配置、注册表、lease、信号、装配
├── internal/cli/        命令解析与只读展示
├── internal/archtest/   依赖/纯度/别名约束测试
├── internal/lease/      Unix/Windows 文件租约
├── cmd/harness/         main → cli.Main
└── docs/design/         本目录
```

## 4. 依赖方向

| 包 | 允许依赖 | 禁止 |
|---|---|---|
| `core` | 标准库 | 任何第三方面；`os`/`os/exec`/`net`/`crypto/rand`/`time.Now`（非测试） |
| `engine` | `core` + 标准库 | 任何适配器、`runtime`、`internal/cli`；环境时钟/随机/环境变量/文件系统 |
| `pi`、`sandbox/docker`、`verify`、`store/sqlite` | `core`/`engine`/标准库/第三方 | 其他适配器、`runtime`、`internal/cli`、模块根 |
| `runtime` | 全部适配器 + `engine`/`core` | —（组合根） |
| `internal/cli` | `runtime` + 只读端口 | 适配器内部实现、engine 私有状态 |
| `cmd/harness` | `internal/cli` | — |

由 `internal/archtest` 强制：

- `core` 非测试文件只导入标准库。
- `engine` 非测试文件只导入 `core` + 标准库，且不含 `time.Now`、`crypto/rand`、`os.*`、文件系统调用。
- 适配器之间禁止互相导入；适配器禁止导入 `runtime`/`internal/cli`。
- 生产代码禁止跨包类型别名（`type X = otherpkg.X`），测试除外。
- 根目录不再有 `harness.go` facade。

## 5. 公开 API

- 公开 SDK 表面：`core`（领域类型与校验）+ `engine`（ports 与 Runner）。
- `runtime` 是组合根，面向 CLI 与嵌入式调用方，但不是领域契约。
- 适配器包保持现有路径：`pi/`、`sandbox/docker/`、`verify/`、`store/sqlite/`。
- 删除根 `harness.go` 与 `engine/core_aliases.go`；同一符号只保留一个 import 路径。
- 用户自定义适配器实现 `engine` 的端口；适配器可自由导入 `core`/`engine`。

## 6. 组件职责

| 组件 | 职责 | 不负责 |
|---|---|---|
| `core` | 类型、校验、状态机、记录格式、链哈希、证据规则、脱敏 密封 | I/O、时钟、进程、存储 |
| `engine` | 单 Run 生命周期、board、Reason/Explore、预算、仲裁、恢复规划、finalize | 进程启动、环境变量、文件系统、SQL |
| `pi` | Pi JSONL RPC、session 生命周期、事件归一化 | 领域状态、预算、成功判定 |
| `sandbox/docker` | Target 供应、容器启动/清理、孤儿清理 | 目标身份授权（由 core 校验） |
| `verify` | 执行外部验证器、解析 Verdict | 成功判定之外的领域规则 |
| `store/sqlite` | journal、读模型、artifact blob、lease、回放、备份/切换 | 领域校验（复用 core） |
| `runtime` | 配置、注册表、lease、信号、依赖装配、生命周期 | 领域规则 |
| `internal/cli` | 命令、输出格式、只读展示 | 领域状态机 |

## 7. 控制流

```text
runtime.OpenWriter (lease)
  -> engine.New(cfg)
  -> engine.Start(manifest)
       -> IDs.NewRunID
       -> Journal.Create(manifest, run.created)         [tx]
       -> TargetProvisioner.Provision
       -> Journal.Append(run.phase=provisioning/running) [tx]
       -> board loop:
            Reason  -> reason.* records
            Explore -> attempt.*, observation.*, artifact.stored, claim.*
            Append(batch)                               [tx: journal + state + read models]
       -> goal_candidate && clean
       -> Journal.Append(run.phase=verifying)           [tx]
       -> Verifier.Verify(evidence bundle)
       -> Journal.Finalize(verdict, run.finalized)      [tx: journal + verdict + finalization + state + checkpoint]
  -> engine.Close
runtime.Close (release lease)
```

关键点：

- 每次状态变更是一次带 `ExpectedHead` 的原子 append；不存在 `GetRun + UpdateRun + AppendEvent`。
- 只有 `Journal.Finalize` 写 terminal；成功终结必须有成功 verdict。
- 所有进入存储的内容已由 `core.Redactor` 密封。
- 所有 goroutine 根由 `safego.Go` 包裹；panic 收敛为 control error 并 finalize 为失败。

## 8. 非目标与边界声明

- 本地 SQLite 不是对抗性信任边界：拥有文件读写权限的人可以改库；v5 提供 append-only 约束、per-run 链与 `verify-chain` 用于发现篡改，不承诺防恶意管理员。
- 脱敏 只保证"已知 secret 值不落盘"，不保证识别所有敏感信息；启发式规则是尽力而为。
- `caller_managed` 隔离不做网络 ACL；调用方对容器网络负责。
- 发布版本与模块路径在 Phase 0 不定；见 [ADR 0006](adr/0006-release-versioning-deferred.md)。

## 9. 与其他文档的关系

- 性质与执行点：[invariants.md](invariants.md)
- 生命周期：[state-machine.md](state-machine.md)
- 存储与 schema：[data-model.md](data-model.md)
- 端口签名：[ports.md](ports.md)
- 恢复：[recovery.md](recovery.md)
- 权威与脱敏：[verification-redaction.md](verification-redaction.md)
- 迁移与切换：[migration.md](migration.md)
