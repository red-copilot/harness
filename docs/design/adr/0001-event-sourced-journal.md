# ADR 0001: 采用 per-run 事件日志作为唯一真相

- Status: Proposed (v5)
- Date: 2026-11-25
- Deciders: Red Harness maintainers
- Related: [data-model.md](../data-model.md), [invariants.md](../invariants.md)

## Context

v4 的持久化同时存在三种表示：

1. 规范化实体表（`claims`/`intents`/`attempts`/`observations`/`artifacts`）保存 typed 列 + opaque JSON `payload`；
2. `board_commits` 保存无版本的 `engine.Commit` JSON 快照；
3. `events` 保存规范化事件，并维护一条**全局** SHA-256 链。

读取路径只使用 JSON payload；快照与实体表可能漂移；restore 只校验最新快照的少量条件；事件链跨 Run 共享头，依赖"单进程写者"的约定。`updatePhase` 还是 `GetRun + UpdateRun + AppendEvent` 三步，崩溃可产生不一致。

这违反了 v5 的 I5（单一、可回放、单写者）与 I3（终结不可部分写入）。

## Decision

采用 **per-run 事件日志（journal）作为唯一真相**：

- `run_journal(run_id, seq, type, schema_version, at, payload, prev_hash, hash)` 是规范历史；
- `run_state`、`run_checkpoints`、`rm_*` 是可重建投影；
- 一个 `core.Record` 信封 + 每种类型一个 payload struct；
- per-run 哈希链，genesis = manifest digest；
- 每次语义变更是带 `ExpectedHead` 的单事务 append；finalize 是单事务；
- 日志 `run_logs` 独立、追加、不进链、不参与回放；
- schema 5，通过备份→验证→OpenFresh 切换。

## Consequences

### Positive

- 回放是确定性的；`doctor verify-chain` 能发现任一字节篡改。
- 不再有"实体表 / payload / 快照"三套可漂移表示。
- phase、board、usage、verdict、finalization 在同一事务内原子落盘。
- 读模型可删除重建；适配器实现变简单（一个写入口）。
- 不同 Run 之间没有共享链头，消除跨 Run 写者耦合。

### Negative / costs

- 需要 checkpoint 与回放实现；P3/P4 的工作量集中在 store 与 kernel。
- schema 5 是单向磁盘边界，需要 cutover 与回滚演练。
- 日志与事件分离需要明确的停泵顺序（finalize 前停止日志）。
- 大 Run 的 journal 可能较大；需要 checkpoint 阈值与基准。

### Neutral

- 记录类型注册表成为新的兼容面；新增类型需要版本与解码器。
- 回放性能取决于 checkpoint 频率与 board Apply 的复杂度。

## Alternatives considered

1. **权威 state + 原子 journal（非事件溯源）**：更简单，但仍需定义 state 的多个写路径，且难以审计"为什么变成这样"。拒绝。
2. **保留规范化表 + 快照，仅补原子性**：能修部分问题，但三套表示与全局链的漂移仍在。拒绝。
3. **全量事件溯源，日志也进链**：审计最强，但日志体量大、链与回放成本高，且日志不是审计真相。拒绝。
4. **只存最终 Result，不存过程**：无法恢复、无法审计证据链。拒绝。

## References

- v4 证据：`store/sqlite/store.go`（schema）、`store/sqlite/commit.go`（board_commits）、`store/sqlite/audit.go`（全局链）、`store/sqlite/restore.go`（浅校验）、`engine/run_controller.go:416-446`（非原子 phase）。
- v5 目标：[data-model.md](../data-model.md)、[state-machine.md](../state-machine.md)、[ADR 0007](0007-schema-v5-cutover.md)。
