# v5 状态机

> **状态：v5 目标设计（提案）。** 当前代码仍是 v4 / schema 8。

## 1. 阶段与结果

阶段（phase）：

| Phase | 含义 | 允许的副作用 |
|---|---|---|
| `queued` | manifest 与 journal 已创建，尚未 provision | 无 target、无 session |
| `provisioning` | 正在创建/绑定 Target | 仅 target provisioner |
| `running` | Reason/Explore 循环 | 一个 Executor attempt、一个 Reason turn |
| `verifying` | 证据打包与外部验证 | 恰好一次 `Verifier.Verify` |
| `terminal` | 已写入 finalization | 不再有 journal/log 写入 |

结果（outcome，仅 terminal）：

`succeeded | failed | cancelled | timed_out | interrupted | stagnated`

`outcome != ""` 当且仅当 `phase == terminal`。`succeeded` 必须有成功 verdict。

## 2. 转移矩阵

| From | 事件 | 守卫 | To | 持久化 |
|---|---|---|---|---|
| `queued` | start | manifest 校验通过；Target 身份已授权 | `provisioning` | `run.phase` |
| `queued` | finalize(非成功) | 匹配 finalization | `terminal` | `run.finalized` + finalization |
| `provisioning` | target ready | `ValidateTargetBinding` 通过 | `running` | `run.phase` + `target.provisioned` |
| `provisioning` | finalize(非成功) | 允许 | `terminal` | `run.finalized` |
| `provisioning` | resume | 重新 provision，身份/隔离不可变 | `provisioning` | `run.resumed` |
| `running` | board round | 单槽；预算未超 | `running` | intent/attempt/claim/observation/artifact 记录 |
| `running` | goal_candidate | 无活跃 attempt；无 open intent；无 control/budget/cleanup 错误 | `verifying` | `run.goal_candidate` + `run.phase` |
| `running` | fatal control/exploration error | — | `terminal(failed)` | `run.finalized` |
| `running` | cancel | ctx canceled | `terminal(cancelled)` | `run.finalized` |
| `running` | deadline | ctx deadline | `terminal(timed_out)` | `run.finalized` |
| `running` | stagnation | 见第 4 节 | `terminal(stagnated)` | `run.finalized` |
| `running` | resume | journal 可回放 | `running` | `run.resumed` + 归一化记录 |
| `verifying` | verdict success | 无 control/cleanup 错误；ctx 未取消 | `terminal(succeeded)` | `run.finalized` + verdict + finalization |
| `verifying` | verdict failure / verifier error | — | `terminal(failed)` | `run.finalized` + verdict(可选) |
| `verifying` | cancel / deadline | — | `terminal(cancelled / timed_out)` | `run.finalized` |
| `verifying` | resume 且无 terminal | journal 可回放 | `verifying` | `run.resumed`；重新验证一次 |
| `terminal` | 任何事件 | — | 拒绝 | — |

非法转移一律返回类型错误，不写入 journal。

## 3. 持久化规则

1. 每次 phase 变更是一次 `Journal.Append`，携带 `ExpectedHead` 与 `run.phase` 记录；不存在 `GetRun + UpdateRun + AppendEvent`。
2. 只有 `Journal.Finalize` 能写 `terminal`。`Append` 在 `phase == terminal` 时被 Go 与触发器双重拒绝。
3. `run_state.head_seq/head_hash` 是 CAS 令牌；append 必须匹配，否则 `ErrStaleHead`。
4. phase 不允许回退；同一阶段内的多次 append 合法（board round）。
5. terminal 后禁止 journal/log 写入；finalize 必须在同一事务内完成 verdict、finalization、state、checkpoint。
6. `run_state` 是 journal 的投影；如果两者不一致，以 journal 回放结果为准，并将数据库标记为损坏、fail closed。

## 4. 停滞（stagnation）

定义：

- 一个 board round 结束后，如果没有新增 Claim，且没有 open/claimed Intent，则 `NonGrowthRounds++`；任何新增 Claim 将 `NonGrowthRounds` 清零。
- 满足任一条件即 `stagnated`：
  - `NonGrowthRounds >= Policy.Exploration.StagnationRounds`；
  - Reason 返回零 Intent 且 Executor 槽空闲；
  - Reason 失败/拒绝次数达到 `Policy.Exploration.MaxReasonerFailures`；
  - `MaxTotalIntents` 已耗尽且没有 goal_candidate。
- `stagnated` 是终态非成功；**永不调用 verifier**，也不允许被 verifier 的结论覆盖。

## 5. 验证触发

进入 `verifying` 的唯一原因是 `goal_candidate`：

- Reason 明确表示当前 Claim 图已足以回答目标；
- 当前没有活跃 attempt；
- 当前没有 open intent（goal_candidate 后不再接纳新 Intent，但已有 open Intent 继续执行）；
- 没有 control error、budget breach、cleanup error；
- 运行上下文未取消/超时。

`goal_candidate` 不是成功，只是"请求验证"。如果 verifier 返回失败，Run 仍是 `failed`。

## 6. 恢复时的归一化

`Resume` 按持久化 phase 分派，并在同一事务中写入归一化记录：

| 持久化 phase | 归一化 | 下一步 |
|---|---|---|
| `queued` | 无 | 重新 start |
| `provisioning` | 无 | 重新 provision（runtime 细节可变，身份/隔离不可变） |
| `running` | 所有 `running` attempt → `interrupted`；所有 `claimed` intent → `open`；清空 reason-in-flight | 恢复 board，重新开始 Reason/Explore |
| `running` 且 `Stagnated=true` | 同上 | 直接 finalize `stagnated`，不再探索 |
| `running` 且 `GoalCandidate=true` | 同上 | 处理完 open Intent 后进入 `verifying`，验证 |
| `verifying` | 无 | 没有 terminal 时重新执行一次验证（finalize 是原子的，不存在"有 verdict 无 terminal"的持久状态） |
| `terminal` | — | 拒绝 resume |

恢复不使用调用方提供的 task/target/policy；只允许提供与 manifest 匹配的 session/target/verifier 绑定。

## 7. 失败映射

| 情况 | outcome |
|---|---|
| control error（persistence/panic/cleanup/protocol） | `failed` |
| 运行 ctx canceled | `cancelled` |
| 运行 ctx deadline | `timed_out` |
| 预算（wall/token/model/tool/cost）超限 | `failed` |
| exploration 致命错误（不可恢复的解析/证据/artifact 失败） | `failed` |
| 停滞/零 intent/reasoner 失败上限/方向耗尽 | `stagnated` |
| verifier error / `Success=false` | `failed` |
| verifier panic | `failed` |
| 操作者 `runs interrupt` | `interrupted` |
| 无 goal_candidate 的干净停止 | `failed`（若停滞条件成立则 `stagnated`） |

## 8. 非法状态与拒绝

以下状态必须被类型/触发器/校验拒绝：

- `terminal` 且无 `run_finalization`；
- `succeeded` 且无 `success=1` verdict；
- `outcome != ""` 且 `phase != terminal`；
- phase 回退或跳跃（除矩阵允许的边）；
- terminal 后的 journal/log 写入；
- 两个活跃 attempt 或两个已 claim intent；
- finding/negative Claim 无 Observation；Observation 引用不存在/未授权 Artifact；
- 同一事务内出现多个 finalize 记录。

## 9. 测试矩阵

- 表中每一条合法边：成功写入并投影。
- 每一条非法边：返回类型错误，journal 不变。
- 每个持久化 phase 的 resume：正确分派；running attempt 归一；claimed intent 重开。
- 停滞：三种触发路径；确认 verifier 调用次数为 0。
- 验证：goal_candidate 才验证；成功/失败/error/panic；恰好一次。
- 并发：同一 Run 两个 append，只有一个成功；resume 与 interrupt 并发，只有一个成功。
- 崩溃注入：finalize 各步骤失败，数据库不出现"terminal 无 finalization"或"succeeded 无 verdict"。
