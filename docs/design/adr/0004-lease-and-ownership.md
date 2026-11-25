# ADR 0004: 写者租约下沉到存储层，中断使用 CAS

- Status: Proposed (v5)
- Date: 2026-11-25
- Deciders: Red Harness maintainers
- Related: [recovery.md](../recovery.md)

## Context

v4 的进程隔离只在 CLI 层：

- `internal/cli/recovery_guard.go` 用 Unix-only `syscall.Flock` 锁 `<db>.lock`，没有 build tags，导致 Windows 构建失败；
- 库调用者不经过 CLI，因此没有任何写者互斥；
- `NewRunner` 无条件批量 `MarkInterrupted`，CLI 却先构造 Runner 再 `Resume`，导致 resume 必然失败；
- `MarkRunInterrupted` 不校验调用者是否持锁，也不校验 head/terminal；
- `runs cancel` 是唯一遵守协议的命令。

这违反了 I8（同一 Run 只有一个写者，resume 只用持久化状态）。

## Decision

1. 把租约下沉到 `store/sqlite.OpenWriter`：获取 `<db>.lock` 并持有到 store 关闭；库调用者与 CLI 同等受保护。
2. `internal/lease` 提供 build-tagged 实现：Unix `flock`，Windows `LockFileEx`；公共代码禁用 `syscall.Flock`。
3. `OpenReader` 以只读、`query_only` 打开，不取租约；所有读命令使用它。
4. `Runner.New` 不做任何恢复、不批量中断、不隐式清理孤儿。
5. 中断是显式 CAS 操作：`Recovery.Interrupt(runID, expectedHead, reason)`，校验 head、terminal、进程内 active-run 表，在一个事务内 append + finalize。
6. 启动策略显式：`RecoveryNone`（默认）、`RecoveryInterruptOrphans`（显式）、`RecoveryResume`（点名 run id）。
7. resume 只使用持久化 manifest；绑定只用于重建外部资源。

## Consequences

### Positive

- 库调用者自动获得进程隔离；不再依赖 CLI 包装。
- resume 与中断不再竞态；过期命令因 head 不匹配而失败。
- Windows 构建与运行可行。
- 启动不再有隐式破坏性行为。

### Negative / costs

- 破坏性行为变更：不再"启动即中断"所有非终态 Run；操作者需要显式处理。
- 需要引入 `internal/lease` 与平台测试。
- 只读打开的 SQLite 需要 WAL 与并发读验证。

### Neutral

- 租约文件是诊断信息，不是正确性来源；OS 锁在崩溃时自动释放。
- 本地数据库仍不是对抗性边界；租约防并发，不防直接改库。

## Alternatives considered

1. **心跳租约**：更复杂，且在 OS 文件锁已足够时没有必要。拒绝。
2. **仅数据库行锁**：SQLite 没有跨连接的持久写者所有权语义；崩溃后难以判断。拒绝。
3. **保持 CLI-only flock，补 build tags**：库调用者仍不受保护，resume 竞态仍在。拒绝。
4. **每 Run 一个锁文件**：会产生大量锁文件与清理问题；数据库级租约已足够（一个库一个写者）。拒绝。

## References

- v4 证据：`internal/cli/recovery_guard.go:25`、`engine/runner.go:63-74`、`internal/cli/cli.go:105-116`、`store/sqlite/store.go:458-494`。
- v5 目标：[recovery.md](../recovery.md)、[ports.md](../ports.md) 的 `Recovery`。
