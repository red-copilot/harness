# v5 恢复、所有权与进程隔离

> **状态：v5 目标设计（提案）。** 当前代码仍是 v4 / schema 8。

## 1. 所有权模型

一个 Run 同时只能有一个写者。所有权分两层：

1. **数据库写租约**：同一数据库路径同时只有一个写进程。由 `store/sqlite.OpenWriter` 获取 `<db>.lock` 并持有到 store 关闭；库调用者与 CLI 一样受保护。
2. **进程内 active-run 表**：同一进程内同一 run ID 只能有一个 `*engine.Run`。由 `Runner` 的 map 与 CAS 保证。

读命令使用 `OpenReader`（`mode=ro&_pragma=query_only(1)`），不获取租约，可与写者并发。

## 2. 租约实现

- 路径：`<db>.lock`，文件权限 0600，目录 0700。
- Unix：`//go:build unix`，非阻塞 `flock(LOCK_EX|LOCK_NB)`；进程崩溃由 OS 释放。
- Windows：`//go:build windows`，`LockFileEx` + `LOCKFILE_EXCLUSIVE_LOCK|LOCKFILE_FAIL_IMMEDIATELY`。
- 租约文件写入诊断信息：pid、hostname、启动时间、可执行路径；不参与正确性判断。
- `OpenWriter` 获取失败返回 `ErrLeaseHeld`，错误包含路径与诊断信息。
- 不做心跳；OS 锁是唯一所有权真相。
- 公共代码禁止 `syscall.Flock`；平台差异只在 `internal/lease` 的 build-tagged 文件中。

## 3. 启动策略

`Runner.New` **不做任何恢复**，不批量 `MarkInterrupted`，不隐式清理孤儿。恢复是显式操作：

| 策略 | 行为 | 默认 |
|---|---|---|
| `RecoveryNone` | 什么都不做；操作者用 `runs list` 查看非终态 | 是 |
| `RecoveryInterruptOrphans` | 持租约后，对所有非终态 Run 逐个 CAS 中断 | 否；需显式 flag |
| `RecoveryResume` | 只恢复明确点名的 run id；manifest/绑定不匹配则失败 | 否；需显式 flag |

没有"启动即破坏性清理"的默认。v4 的 `NewRunner` 批量中断必须删除。

## 4. 中断协议

```text
Recovery.Interrupt(ctx, runID, expectedHead, reason)
  BEGIN IMMEDIATE
    state = SELECT run_state WHERE run_id=?
    if state.phase == terminal        -> ErrTerminal
    if state.head_seq != expectedHead -> ErrStaleHead
    if activeRun(runID)               -> ErrRunActive
    record = chain(type=run.interrupted, reason)
    INSERT run_journal(record)
    INSERT run_finalization(outcome=interrupted, journal_seq=record.seq)
    UPDATE run_state -> terminal(interrupted)
    INSERT run_checkpoints
  COMMIT
```

- 跨进程竞争由写租约排除；进程内竞争由 active-run 表排除；过期命令由 `expectedHead` 排除。
- 中断是终态，不可回滚；`runs interrupt` 必须在输出中回显 run_id、head、reason。
- v4 的 `MarkInterrupted`/`MarkRunInterrupted` 在 P9 删除。

## 5. 恢复协议

```text
RunResume(ctx, runID, bindings)
  1. OpenWriter -> lease
  2. manifest = Reader.Manifest(runID); Validate
  3. bindings 必须匹配 manifest：session runtime ID/digest、target provisioner、
     verifier ID/digest；不允许覆盖 task/target/policy
  4. restored = Recovery.Replay(runID)
  5. 同一事务 append run.resumed + 归一化记录
  6. 按 restored.State.Phase 分派（见第 6 节）
  7. 重新创建 workspace、target、两个 session
  8. 继续 Reason/Explore 或 Verifying
  9. Finalize 原子落盘
```

`bindings` 只提供"如何重新创建外部资源"，不提供 Run 语义。调用方无法通过 resume 改变目标、预算或策略。

## 6. 按 phase 分派

| Phase | 恢复动作 |
|---|---|
| `queued` | 不重新 Create；从 provisioning 开始 |
| `provisioning` | 重新 `Provision`；runtime 细节可变，TargetID/Addresses/Isolation 不可变；`ValidateTargetBinding` |
| `running` | 恢复 board；启动 session；从下一个 Reason/Explore 继续 |
| `running` + `GoalCandidate` | 归一化后进入 `verifying`，执行验证 |
| `running` + `Stagnated` | 归一化后直接 finalize `stagnated`；不再探索 |
| `verifying` | 没有 terminal 时重新执行一次验证（finalize 原子，不存在"有 verdict 无 terminal"） |
| `terminal` | 拒绝 |

归一化（与 `run.resumed` 同事务）：

- 所有 `running` attempt → `interrupted`；
- 所有 `claimed` intent → `open`；
- reason-in-flight 清零；下次循环重新 Reason；
- 不重放模型输出，不自动重试已失败的 attempt。

## 7. 回放校验清单

`Recovery.Replay` 必须逐项校验，任一失败返回 `ErrCorruptJournal`：

1. manifest schema/digest 正确；
2. `run_state` 的 phase/outcome/head 合法；
3. journal seq 从 1 连续到 head；
4. 每条 `prev_hash` 与前一条 `hash` 相连；seq=1 的 prev 等于 manifest digest；
5. 每条记录的类型/版本可解码；
6. phase 转移符合 [state-machine.md](state-machine.md)；
7. terminal 之后没有记录；
8. board 引用完整：claim→observation→artifact、intent→source claims、attempt→intent；
9. artifact `ID == SHA256 == blob hash` 且 size 一致；
10. checkpoint 的 `state_sha256`/`head_hash` 与 journal 匹配；
11. board revision 单调；
12. 回放得到的 state 与 `run_state` 一致。

失败时**不修复、不猜测、不跳过**；操作者只能 `runs interrupt` 或从备份恢复。

## 8. 崩溃矩阵

| 崩溃点 | 持久化状态 | 下次启动 |
|---|---|---|
| Create 事务前 | 无 manifest/journal | 无 Run；重新创建 |
| Create 事务后 | `queued` | resume 从 provisioning 开始 |
| Provision 中 | `provisioning` | 重新 provision；旧容器由孤儿清理处理 |
| Attempt 执行中 | `running` + `running` attempt | 归一化为 interrupted，intent 重开，继续 |
| goal_candidate 后、phase append 前 | `running` + `GoalCandidate` | 进入 verifying |
| verifying、verifier 调用前 | `verifying` | 重新验证一次 |
| verifier 返回、finalize 事务前 | `verifying`，无 verdict | 重新验证一次 |
| finalize 事务中 | 全有或全无 | 全有 → terminal；全无 → 重新验证 |
| finalize 后 | `terminal` | 拒绝 resume |
| 日志写入与 finalize 竞争 | 日志可能是 terminal 前的最后几条 | 日志泵必须在 finalize 前停止；触发器兜底拒绝 terminal 后写入 |

## 9. 操作命令

| 命令 | 租约 | 说明 |
|---|---|---|
| `run --config` | 写 | 新建 Run |
| `run --resume RUN` | 写 | 按 manifest 恢复；需匹配绑定 |
| `runs list/show/events/logs/board/artifacts` | 只读 | 不阻塞写者 |
| `runs interrupt --id --expect-head --reason` | 写 | CAS 中断；失败回显当前 head |
| `runs resume` | 写 | 与 `run --resume` 相同，操作面区分 |
| `db verify-chain --id` | 只读 | 校验 per-run 链 |
| `db rebuild-readmodel --id` | 写 | 删除并重建 `rm_*` |
| `db backup/verify-backup/cutover` | 写 | 见 [migration.md](migration.md) |
| `doctor` | 只读 | schema、链、lease 文件、配置 |

退出码建议：

- `0` 成功；
- `1` Run 非成功（verdict failure）；
- `2` 用法/配置错误；
- `3` 所有权/head 冲突（`ErrLeaseHeld`/`ErrStaleHead`/`ErrTerminal`）；
- `4` 数据损坏（`ErrCorruptJournal`）。

## 10. 安全说明

- 本地 SQLite 文件不是对抗性信任边界。拥有文件写权限的人可以绕过触发器；`VerifyChain` 用于发现篡改，不用于防御恶意管理员。
- 写租约防止并发写者，不防止直接改库。
- 恢复失败必须 fail closed；任何"尽力修复"都会破坏 I5/I8。
