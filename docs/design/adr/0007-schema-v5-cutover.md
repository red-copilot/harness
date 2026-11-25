# ADR 0007: schema v5 采用备份→验证→OpenFresh 单向切换

- Status: Proposed (v5)
- Date: 2026-11-25
- Deciders: Red Harness maintainers
- Related: [data-model.md](../data-model.md), [migration.md](../migration.md)

## Context

v5 的持久化模型与 v8 根本不同：event-sourced journal、per-run 链、派生读模型、新触发器。v8 的 `Open` 明确拒绝非 8 版本，仓库已确立"备份→验证 manifest→OpenFresh"的切换模式，并声明这是一条单向边界。

直接原地迁移（ALTER/数据搬运）会引入复杂的双写/回滚逻辑，且难以证明迁移后的链与 board 一致。

## Decision

采用现有的单向切换模式：

1. 停止所有 v4 写者，确认 `<db>.lock` 无持有者。
2. v4 `db backup` 生成副本与 manifest：SHA-256、大小、schema 8、行数、事件头、artifact 字节总数。
3. 独立进程 `db verify-backup`；任何不匹配即失败。
4. 把 v8 库与 `-wal`/`-shm` **移动**到归档目录，不删除。
5. 在同目录创建临时 v5 库，fsync，原子 rename 到 `live.db`；写 `cutover.json` 记录两侧 digest、时间、操作者。
6. v5 拒绝 v8；`doctor` 验证 schema 5、空历史、链、lease。
7. 冒烟 Run 必须只通过 verifier 成功；`db verify-chain` 通过。
8. 回滚：停止 v5，归档 v5 库，恢复已验证的 v8 备份与 sidecar，启动 v4。

## Consequences

### Positive

- 切换步骤可验证、可回滚；不需要在旧库上做不可逆的原地迁移。
- v8 历史有独立、可校验的备份；归档保留原始文件。
- v5 新库从空历史开始，链与读模型没有迁移噪声。
- 与仓库既有的 `OpenFresh` 语义一致。

### Negative / costs

- 需要停机窗口与双人复核。
- 回滚会丢失 cutover 之后产生的 v5 Run（它们保留在 v5 归档，但不可导入 v4）。
- 需要维护 v4 备份/验证工具直到 P9 完成。

### Neutral

- 备份是逻辑一致性副本；必须独立验证，而不是信任生成方。
- `VACUUM INTO` 与文件复制都需要考虑 WAL/SHM。
- 本地数据库不是对抗性边界；manifest 防止误操作与损坏，不防恶意管理员。

## Alternatives considered

1. **原地迁移 v8 → v5**：需要复杂的双写、回滚与链重建；难以证明正确性。拒绝。
2. **双读 v8/v5**：长期维护两套 schema 与解码，违背单一真相。拒绝。
3. **丢弃历史，只保留最终 Result**：不可接受，破坏审计。拒绝。
4. **导出/导入 JSON**：可行但引入第二套序列化契约与丢失风险；备份+新库更直接。拒绝。

## References

- v4 切换实现：`store/sqlite/backup.go`、`store/sqlite/store.go` 的 `OpenFresh`。
- v5 目标：[data-model.md](../data-model.md) 第 10 节、[migration.md](../migration.md) 第 5 节。
