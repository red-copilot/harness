# v5 权威与脱敏

> **状态：v5 目标设计（提案）。** 当前代码仍是 v4 / schema 8。

# Part A：仅 Verifier 权威

## 1. 删除的双权威表面

| v4 符号/文件 | 处理 |
|---|---|
| `core.Completion`（`core/run.go`） | 删除 |
| `VerdictAuthority`、`VerdictAuthorityBackend`（`core/types_evidence.go`） | 删除；verdict 不再带权威枚举 |
| `CompletionSource`、`Backend`（`engine/ports.go`） | 删除 |
| backend watcher、`watchBackend`、`completionVerdict`、`waitForBackendWatcher`（`engine/run_controller.go`） | 删除 |
| `EventAuthorityProgress/Completed/ObserveError` 及 authority 配置 | 删除 |
| `README.md:3,19`、`CLAUDE.md`、`AGENTS.md` 的双权威表述 | P9 改为"仅 Verifier" |
| CLI/config 中任何 backend/authority 字段 | 删除；出现即拒绝并指向本文件 |

`Verdict` 形状：

```go
type Verdict struct {
    Success  bool
    Score    float64
    Message  string
    Evidence []ArtifactRef
}
```

verifier 的身份记录在 `verifier.completed` 记录与 `run_finalization`/`verdicts.verifier_id` 中，用于审计，不参与成功判定。

## 2. 仲裁顺序

`core.Arbitrate` 是纯函数，输入是冻结的 Run 快照，输出一个终态决定。顺序固定，第一条命中即返回：

1. 存在 control error（persistence、panic、cleanup、protocol、log 持久化失败）→ `failed`。
2. `ctx.Err() == context.Canceled` → `cancelled`。
3. `ctx.Err() == context.DeadlineExceeded` → `timed_out`。
4. 预算超限（wall/token/model/tool/cost）→ `failed`。
5. 致命 exploration/protocol 错误（不可恢复的解析、证据、artifact、授权失败）→ `failed`。
6. `stagnated` → `stagnated`。
7. 没有 `goal_candidate` 的干净停止 → `failed`（"no goal candidate"）。
8. 有 `goal_candidate` 且无上述问题 → 进入 `verifying`，调用 `Verifier.Verify` 恰好一次：
   - error → `failed`；
   - `Success=false` → `failed`；
   - `Success=true` → `succeeded`。

`core.Arbitrate` 不直接调用 verifier；它返回"需要验证"的决定，由 controller 执行。这样仲裁本身可做表驱动测试。

## 3. goal_candidate 与停滞

- 只有 `goal_candidate` 能进入 `verifying`。Claim 数量、模型文本、退出码、usage 都不能触发验证。
- `goal_candidate` 后停止接纳新 Intent；已有的 open Intent 继续执行；当活跃 attempt 与 open Intent 都清空后进入 `verifying`。
- 停滞、零 intent、reasoner 失败上限、方向耗尽：直接 `stagnated`，**不调用 verifier**。
- 取消、超时：直接 `cancelled`/`timed_out`，不调用 verifier。
- 这修正 v4 中 `arbitrate` 先看 verifier、导致 `stagnated` 几乎不可达的漂移。

## 4. 成功条件

`succeeded` 必须同时满足：

1. 无 control error、无 cleanup error、无预算超限；
2. 到达 `verifying` 的原因是 `goal_candidate`；
3. 证据 bundle 的每个 artifact 已存储、已脱敏、哈希与大小可复验；
4. `Verifier.Verify` 恰好被调用一次且返回 `Success=true`；
5. 成功 verdict 与 finalization、terminal state 在同一事务内持久化；
6. finalize 时运行上下文未被取消。

verifier 返回后如果 cleanup（target/session close）失败，成功降级为 `failed`，但成功 verdict 仍保留在审计中（v4 行为保留）。这是重要的审计语义：verdict 是"验证结论"，outcome 是"Run 结论"；后者更严格。

## 5. Verifier 适配器契约

`verify/` 适配器保持现有路径，v5 契约：

- stdin：一个 JSON 对象，字段 `run_id`、`task`、`target`（绑定后的有效目标）、`evidence_dir`。
- stdout：一个 `Verdict` JSON 对象；未知字段忽略。
- 非零退出、无效 JSON、超时、无法启动 ⇒ error ⇒ Run `failed`。
- 环境变量必须显式配置；不默认继承完整 host 环境（P8 收紧）。
- stdout/stderr 有上限；超出截断并报错，不无限缓冲。
- 不把 verifier 的 stderr 原文直接写入持久化；错误先脱敏。
- verifier 是调用方提供的受信组件；v5 不声称 verifier 自身安全。

resume 在 `verifying` 且无 terminal 时重新验证一次；因为 finalize 是原子的，不存在"已持久化 verdict 但未终结"的状态。

## 6. 测试矩阵

| 场景 | 期望 |
|---|---|
| goal_candidate + verifier success | `succeeded`，verdict 持久化，verifier 调用 1 次 |
| verifier Success=false | `failed`，verdict 持久化 |
| verifier error / 超时 / panic | `failed`，无成功 verdict |
| cancel / deadline | `cancelled`/`timed_out`，verifier 调用 0 次 |
| 预算超限 | `failed`，verifier 调用 0 次 |
| stagnation / 零 intent / reasoner 失败 | `stagnated`，verifier 调用 0 次 |
| 无 goal_candidate 的停止 | `failed`，verifier 调用 0 次 |
| verifier 成功后 target close 失败 | `failed`，成功 verdict 保留 |
| resume 且无 terminal | 重新验证一次 |
| 生产代码 grep | 无 `CompletionSource`/`Backend`/`VerdictAuthorityBackend` |

---

# Part B：脱敏（sealed boundary）

## 7. 威胁模型

必须保证：**已知 secret 值不进入持久化、读模型、日志、verifier 输入或错误信息。**

已知 secret 来源：

- 配置中的 `known_secrets`；
- `secret_env` 指向的环境变量值；
- agent 配置中的内联 `env` 值（非 secret 名称）；
- 调用方显式注入的额外 secret。

不保证：识别所有敏感信息、脱敏未知格式的凭据、防御拥有数据库写权限的恶意管理员。启发式（`flag{...}`、`password=` 等）是尽力而为，不是安全边界。

## 8. Sealed 类型

```go
type SealedManifest struct{ m Manifest }              // 未导出字段
type SealedBatch struct{ b Batch }                    // records + expected head + state delta
type SealedArtifact struct{ a ArtifactInput }         // 内容已脱敏或已拒绝
type SealedLog struct{ l LogRecord }
type SealedFinalization struct{ f Finalization }      // error/verdict/records 已脱敏

func (r *Redactor) SealManifest(m Manifest) (SealedManifest, error)
func (r *Redactor) SealBatch(b Batch) (SealedBatch, error)
func (r *Redactor) SealArtifact(a ArtifactInput) (SealedArtifact, error)
func (r *Redactor) SealLog(l LogRecord) (SealedLog, error)
func (r *Redactor) SealFinalization(f Finalization) (SealedFinalization, error)

func (s SealedBatch) Batch() Batch
func (s SealedArtifact) Artifact() ArtifactInput
func (s SealedLog) Log() LogRecord
func (s SealedFinalization) Finalization() Finalization
```

- 未导出字段 + 构造器只能由 `core.Redactor` 调用 ⇒ 其他包无法伪造"已密封"。
- `Journal.Create/Append/Finalize`、`ArtifactWriter.Put`、`LogWriter.AppendLogs` 的签名只接受 sealed 类型。
- 适配器可以读取内容（通过 getter），但无法构造新的 sealed 值；它们不负责脱敏，只负责持久化。

## 9. 写路径

| 数据 | 密封点 | 存储 |
|---|---|---|
| Manifest | `runtime` 构造后立即 `SealManifest` | `run_manifests.descriptor` |
| 记录/事件 | controller 构造 batch 后 `SealBatch` | `run_journal.payload` |
| 日志 | log pump 每行 `SealLog` | `run_logs.text` |
| 证据文本 | `ArchiveEvidence` 读取后 `SealArtifact` | `artifact_blobs.data` + `artifact.stored` |
| 证据二进制 | 含已知 secret ⇒ 拒绝；否则 `SealArtifact` | 同上 |
| 终结 | `SealFinalization`：error、verdict message/evidence、final records | `run_journal`/`verdicts`/`run_finalization` |
| 错误/panic | `redactError` 后进入 `State.Error`/`control.error` | journal |
| 读模型 | 从已密封记录投影 | `rm_*` |
| verifier 输入 | 从已密封 blob materialize，并重算哈希 | 临时目录，验证后删除 |

## 10. 结构性禁止

- 删除 `engine/redactingStore`（未包裹 `Put`）与 `engine/redaction.go`。
- 删除 `store/sqlite.SaveClaim/SaveIntent/SaveIntentAttempt/SaveBlackboardObservation/SaveArtifact/Put` 等未密封公开写入口。
- CLI 不再把裸 `sqlite.Store` 作为 `Artifacts`/`Store` 传入；只接收 `runtime` 装配的端口。
- arch 测试禁止适配器公开方法以 `core.ArtifactInput`、`core.Batch`、`core.Record` 等裸类型作为写参数。
- `Journal`/`ArtifactWriter`/`LogWriter` 之外不存在写 domain 数据的路径。

## 11. 脱敏 行为

- 已知 secret 值去重后按长度降序替换，避免子串 secret 的替换顺序不确定。
- 文本证据先脱敏再计算 SHA-256；`artifact.stored` 的 `ID` 是脱敏后内容的哈希，`redacted=true`。
- 二进制证据含已知 secret ⇒ 拒绝并记录 `artifact.rejected`（不含原始字节）。
- JSON 脱敏 递归处理字符串与对象键；键冲突使用稳定后缀（`#2`、`#3`）。
- 解析失败时退化为原始字符串替换，绝不"原样放行"。
- 日志行逐条密封；超大行截断并标记。
- panic 堆栈先脱敏再持久化；不得包含 secret 的环境变量值。

## 12. 测试与审计

1. 构造一个包含已知 secret 的 Run，覆盖 task、target、claim、intent、observation、artifact 文本、日志、错误、verifier 输出；对数据库全表扫描，断言 secret 值零出现。
2. 二进制证据含 secret：`Put` 未被调用，Run 失败或证据被拒绝；数据库无原始字节。
3. 未经 `Seal*` 的类型无法传给 `Journal`/`ArtifactWriter`/`LogWriter`（编译期测试 + arch 测试）。
4. 删除 `rm_*` 后重建，读模型仍无 secret。
5. verifier 临时目录在验证后删除；残留检查。
6. `FuzzRedactor`：任意输入 + 随机 secret 集，输出不含 secret 值，且替换可重复。
7. `FuzzEvidencePath`：路径逃逸/符号链接/目录/大小上限一律拒绝。
8. 审计命令 `doctor --scan-secrets`（可选）对数据库做一次已知 secret 扫描。

## 13. 已知边界

- secret 值本身如果只以编码/压缩形式出现，可能不被识别。
- 脱敏 不改变 `artifact.stored` 的语义哈希与内容哈希的对应关系：哈希针对脱敏后的内容；原始内容不持久化。
- verifier 收到的是脱敏后的证据；如果 verifier 需要原始字节，必须由调用方在受信边界内另行提供，不能绕过 sealed 写路径。
