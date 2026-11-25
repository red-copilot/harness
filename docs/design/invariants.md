# v5 不变量

> **状态：v5 目标设计（提案）。** 当前代码仍是 v4 / schema 8。本文档列出 v5 必须永远为真的性质，以及每项性质的结构性执行点。

## 1. 不变量表

| ID | 不变量 | 为什么不可妥协 | 结构性执行点 |
|---|---|---|---|
| I1 | 一个 Run 只针对一个授权 Target；只有一个 Executor 槽；只有一条可写 Claim/Intent 路径 | 多处可写会让审计与证据链失去唯一性 | `core.Manifest.Validate`；`engine.Controller` 单实例；`core.Board` 单槽；`ValidateTargetBinding`；`AuthorizeTarget` |
| I2 | 成功当且仅当 `Verifier.Verify` 返回 `Success=true` | 模型输出、退出码、usage 都不可信 | `core.Arbitrate`；`engine.Controller` 只接受 verifier verdict；`Journal.Finalize` 终态守卫 |
| I3 | 没有 finalization 就没有 terminal；成功终态必须有成功 verdict | 防止"先写终态再补证据"的窗口 | `run_finalization` 触发器；`run_state` terminal 触发器；Go `Finalize` 校验 |
| I4 | 每个 finding/negative Claim 都必须引用当前 Attempt 的 Observation，Observation 必须引用已存储的 Artifact；内容哈希可复验 | 证据链是系统可信度的根 | `core.ValidateAttemptReport`；`core.Board.Apply`；`engine.ArchiveEvidence`；blob 读取时重算 SHA-256 |
| I5 | 规范历史 append-only、per-run 哈希链、单写者、head/revision CAS | 并发写或静默覆盖会破坏审计 | `run_journal` 主键与触发器；`ExpectedHead`；per-run chain；`run_state.head_hash` |
| I6 | 任何持久化字节在进入存储前已脱敏 | secret 一旦落盘不可撤回 | `core.SealedManifest/Batch/Artifact/Log/Finalization`；只能由 `core.Redactor` 构造；`Journal`/`ArtifactWriter`/`LogWriter` 只接受密封类型 |
| I7 | 预算与上限是机械的、fail-closed，永不转化为成功 | prompt 约束不可信 | `core.Policy` 默认值与校验；controller 在仲裁前检查 budget/deadline/tool/byte/artifact 上限 |
| I8 | Resume 只使用持久化 manifest 与 journal；同一 Run 同时只有一个写者 | 调用方覆盖 spec 或双写会产生不一致历史 | `engine.Resume` 只读 manifest；`Recovery.Replay`；`store/sqlite.OpenWriter` lease；进程内 active-run 表 |
| I9 | 持久化、验证、清理、panic 失败一律非成功 | 失败被当成成功是最严重的信任破坏 | control-error 收敛；detached finalize；cleanup 错误并入；`safego.Go` |
| I10 | kernel 确定、可测试：无环境时钟、随机、环境变量、文件系统 | 不可测试的 kernel 无法证明上述性质 | `Clock`/`IDSource`/`Workspace`/`SecretSource` 端口；arch 纯度测试；fake clock/IDs |

## 2. 成功链

成功必须同时满足以下条件，缺一即非成功：

1. Run 未触发 control error、budget breach、cleanup error 或 panic。
2. 到达 `verifying` 的原因是 `goal_candidate`，而不是停滞、取消或超时。
3. 证据 bundle 由已存储、已脱敏、哈希可复验的 Artifact 组成。
4. `Verifier.Verify` 被调用恰好一次，返回 `Success=true`，且没有 error。
5. `Journal.Finalize` 在 `verifying` 阶段写入成功 verdict，并在同一事务中写 finalization 与 terminal state。
6. finalize 时运行上下文未被取消。

反向规则：

- `cancelled`、`timed_out`、`interrupted`、`stagnated` 永不调用 verifier，也不得被 verifier 的"成功"覆盖。
- verifier 返回 `Success=false`、报错、超时或 panic ⇒ `failed`。
- 任何 append/finalize 失败 ⇒ 不允许声称成功；若终态无法落盘，Run 保持非终态并由后续显式中断处理。

## 3. 本质与偶然实现

### 3.1 本质（v5 保留）

- 单写者、revision/head CAS、原子提交。
- 外部权威、失败即非成功。
- Artifact → Observation → Claim 证据链与内容寻址。
- 单槽探索、Reason 最多提议一个 Intent。
- Reason/Explore 分离、结构化有界报告、同 session Conclude。
- 机械预算与上限。
- 已知 secret 脱敏。
- Target 身份的稳定与授权。
- 端口反转、依赖方向。
- 类型化、版本化的规范事件。

### 3.2 偶然实现（v5 丢弃）

- `CompletionSource`/`Backend` 双权威。
- `finalizing` 阶段与 `RunPhaseTerminal` 作为可转移边。
- 21 方法 `Store` 并集与装饰器 `redactingStore`。
- 规范化实体表作为真相、opaque JSON payload、无版本 `board_commits` 快照。
- 全局审计链。
- 调用方 `RunSpec` 恢复、`run:kind:n` 序号解析。
- 双份 usage 记账（controller delta 与 session history）。
- 根 facade、`engine/core_aliases.go` 与三处命名。
- kernel 内的 `ProcessStarter`/`ResourceCleaner`。
- `RuntimeConfig any`、无实现的 `ExternalBackend`、从未发出的 authority progress 事件。

## 4. 当前 v4 的违背点

下表用于证明 v5 的必要性，不作为 v5 行为描述。

| 不变量 | v4 现状 | 证据 |
|---|---|---|
| I2 | 文档宣称 verifier 或 backend 双权威；backend 无生产实现 | `README.md:19`；`engine/ports.go:38-47` |
| I3 | `CreateRun/UpdateRun` 可直接写 terminal；`Finalize` 约束不是结构性的 | `store/sqlite/store.go:189-214` |
| I5 | phase 更新是 Get+Update+Append 三步；审计链全局 | `engine/run_controller.go:416-446`；`store/sqlite/audit.go:25-34` |
| I6 | `redactingStore` 没有 `Put`；CLI 传裸 store 作为 `Artifacts` | `engine/redacting_store.go`；`internal/cli/config.go:138` |
| I8 | `NewRunner` 先批量 `MarkInterrupted`；`Resume` 信任调用方 spec；序号解析缺陷 | `engine/runner.go:63-74,179-259`；`engine/blackboard.go:55-57,320-330` |
| I9 | 无 panic 兜底；`Run.Cancel` 忽略 ctx；`Wait` 硬编码 running | `engine/run_handle.go:18-33`；run goroutine 无 recover |
| I10 | `core`/`engine` 直接调用 `time.Now`、`rand`、`os`、文件系统 | `core/events.go:192`；`engine/util.go:16-54`；`engine/report.go:19-57` |

## 5. 新增或修改不变量的检查清单

任何新不变量必须回答：

1. 它防止的具体错误场景是什么？能否写成一个失败测试？
2. 执行点在 Go 类型、端口签名、SQL 约束还是触发器？至少两层。
3. 失败时是 fail-closed 吗？是否可能被降级为成功？
4. 审计上如何发现违背（`doctor verify-chain`、读模型重建、E2E 扫描）？
5. 是否需要新的记录类型与 schema 版本？若是，按 [data-model.md](data-model.md) 的兼容规则处理。
6. 是否会引入新的可写路径？若是，默认拒绝，除非同时更新单一写边界。
