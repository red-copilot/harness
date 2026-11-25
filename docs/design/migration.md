# v5 迁移、分阶段交付与切换

> **状态：v5 目标设计（提案）。** 当前代码仍是 v4 / schema 8。本文档是 P1–P9 的执行依据。

## 1. 交付模型

- 每个阶段是短生命周期分支（off `main`）→ PR → 门禁 → squash 合并。
- `main` 在每个合并点都必须可构建、`go test ./...` 与 `-race` 通过。
- 不使用长期 `release/v5` 分支；不维护两个 kernel。P4 通过一个临时的 v4 存储桥保持 v8 数据库可运行，只保留一个 kernel。
- 过渡层必须在 P9 删除；每个 PR 描述列出新增/删除的过渡层。
- `docs/design` 描述目标；根 `README.md`/`AGENTS.md`/`CLAUDE.md` 在 P9 前仍以 v4/v8 为准。

## 2. 阶段表

| 阶段 | 目标 | 主要改动 | 门禁 / 退出 | 破坏性 |
|---|---|---|---|---|
| **P0** | 设计文档评审 | `docs/design/*` + `adr/*`；无 Go 改动 | `git diff --check`；文档链接检查；评审签核不变量/schema/状态机/仅 verifier/API | 否 |
| **P1** | 端口与纯度脚手架 | 在 v4 实现上引入窄 `Journal/Reader/Recovery/ArtifactWriter/LogWriter`、`Clock/IDSource/Workspace/SecretSource`；v4 store 适配器；engine 去环境 I/O；arch 纯度测试 | 全量 Go 门禁；无 ambient 调用；行为不变 | Go API 过渡 |
| **P2** | v5 领域与状态机 | 新增 `core/state.go`、`manifest.go`、`record.go`、`board.go`、`evidence.go`、`redact.go`、`policy.go`、`target.go`；旧类型标 deprecated；状态表、`FuzzAdvance`、manifest digest、sealed 类型 | 状态表测试、fuzz、digest golden；旧行为测试仍过 | 新增 API |
| **P3** | v5 journal store | `store/sqlite` 新增 `OpenV5`、journal/readmodel/replay/artifact/lease/cutover；schema 5；per-run 链、checkpoint、读模型重建；独立 conformance/fault/cutover 测试 | porttest、崩溃注入、篡改链、stale head、terminal 守卫、读模型重建、cutover fixture；v8 `Open` 不变 | 新增 API/新库格式 |
| **P4** | v5 kernel + v4 兼容桥 | 用 v5 类型/状态机重写 controller/board/reason/explore/turn/evidence；新增临时 v4 存储桥（v5 records ↔ v8 表）使 main 继续跑 v8 库；resume 正确化、原子 append、terminal-only-finalize、panic 捕获、单 usage | 同一 kernel 在 v5 store 与 v4 桥上跑 porttest + 端到端；resume/kill-restart/并发测试；行为更新经评审 | Go API/行为 |
| **P5** | 仅 verifier 权威 | 删除 backend 全套与 authority 事件/类型；verifier 必填；按 [verification-redaction.md](verification-redaction.md) 实现仲裁与 stagnation | authority 矩阵全表；生产符号 grep；cancel/stagnate/timeout 永不成功 | Go API/行为 |
| **P6** | 密封脱敏边界 | `SealedManifest/Batch/Artifact/Log`；所有写路径密封；artifact Put 密封；删除 redactingStore/redaction/裸写入口；CLI 不再传裸 store | 全字段植入 secret E2E 零泄漏；二进制证据拒绝；fuzz；arch 测试禁止裸写签名 | Go API |
| **P7** | Runtime/CLI/API 清理 | 新增 `runtime` 组合根（Open/OpenRead/Recover/Close、严格配置、注册表、信号）；删除根 facade 与别名层；适配器保留路径；SIGINT/SIGTERM；defer Runner.Close；只读命令 OpenReader；Windows lease | CLI golden；嵌套 typo 拒绝；信号退出；只读无 lease；Windows build；依赖/别名 arch 测试 | 公开 API/CLI |
| **P8** | 收尾加固 | Docker capability allowlist（拒绝 ALL/SYS_ADMIN）、拒绝 `network:host`、挂载目标与符号链接校验、compose override 清理、agent 出网默认限制；修 `examples/verify.sh` 为 `evidence_dir`；严格时间解码；schema_meta 单行；fuzz/CI Windows | 故障矩阵、fuzz ≥30s、`-race`、Windows build、Docker 集成、安全表测试 | 部分配置 |
| **P9** | 切换与删除 v4 | 默认切到 v5；config `harness/v5`；执行备份→验证→OpenFresh；删除 v4 桥/store/旧类型/旧测试；README/AGENTS/CLAUDE 更新到 schema 5/Manifest v5；release workflow 确保 Windows 目标可构建 | 生产库 v5、审计为空、链可验；cutover 与回滚演练；全量 Go 门禁 + Windows + 集成 | 磁盘格式单向 |

## 3. 兼容层清单

| 过渡层 | 引入 | 删除 | 说明 |
|---|---|---|---|
| v4 端口适配器（v4 类型 → 窄端口） | P1 | P4（由 v4 桥替代） | 不是长期层；P4 起由桥统一 |
| v4 存储桥（v5 records ↔ v8 表） | P4 | P9 | 单个 kernel 同时跑 v8 默认库；porttest 必须通过 |
| 旧 `core` v4 类型与其别名 | P2 标 deprecated | P9 | 新类型与旧类型并存；禁止新增引用 |
| `harness/v4` 配置映射 | P7（严格化的 v4 配置） | P9 | P9 切到 `harness/v5` |
| v8 只读/备份/校验工具 | P3 | P9 | 仅用于切换与回滚 |

规则：

- 每个 PR 描述必须写"新增过渡层 / 删除过渡层"。
- 过渡层禁止被新代码引用；arch 测试或 grep 门禁检查。
- P9 的退出条件包括：grep 不到 v4 桥、旧类型、`harness/v4` 映射、v8 写路径。

## 4. 各阶段细节

### P0 — 设计文档（本次）

- 交付：本目录 8 份文档 + 7 份 ADR。
- 门禁：`git diff --check`；文档内部链接有效；不声称跑过 Go 矩阵（docs-only）。
- 退出：评审签核第 2 节列出的六项；未签核不进入 P1。
- 停止点：P0 合并后停止，等待新的实施指令。

### P1 — 端口与纯度脚手架

- 在现有 v4 类型上定义窄端口；实现一个 v4 `storeAdapter` 满足它们。P4 的 v5 kernel 会以 v4 桥替换它，因此它不是独立的长期层。
- `engine.Config` 注入 `Clock/IDs/Workspace/SecretSource`；默认实现放在 `runtime`（P1 可先放在包内但不得在 engine 直接调用环境 I/O）。
- 从 engine 移除 `ProcessStarter`/`ResourceCleaner`；进程启动与孤儿清理上移。
- 新增 arch 纯度测试：non-test engine 无 `time.Now`、`crypto/rand`、`os.*`、文件系统；core 仅 stdlib。
- 退出：行为不变；全量门禁通过。

### P2 — v5 领域与状态机

- 新文件与旧文件并存；旧类型标 `// Deprecated: v5 uses ...`。
- `Advance` 表驱动；`FuzzAdvance` 覆盖非法转移；manifest canonical JSON + digest golden。
- sealed 类型只允许 `Redactor` 构造；先不做写路径切换。
- 退出：旧行为测试仍过；新状态机测试通过。

### P3 — v5 journal store

- 新 `OpenV5`/`JournalV5`/`ReaderV5`/`RecoveryV5`；schema 5；per-run 链；checkpoint；读模型；artifact；日志；lease；cutover 工具。
- 现有 `Open`（v8）保持不变；默认仍 v4。
- 退出：`engine/porttest` 全绿；崩溃注入/篡改链/stale head/terminal 守卫/读模型重建/cutover fixture 通过。

### P4 — v5 kernel + v4 兼容桥

- 单 kernel 重写，使用 v5 类型；v4 桥把 v5 records/state 映射到 v8 的 `events`/`board_commits`/`runs`，使 main 继续跑 v8 库。
- 行为更新：resume 正确、原子 append、terminal-only-finalize、panic 捕获、单 usage、attempt timed_out。
- 退出：同一 porttest 在 v5 store 与 v4 桥上通过；kill-restart resume；并发 append/resume/interrupt；端到端对照。

### P5 — 仅 verifier 权威

- 删除 backend 表面；verifier 必填；仲裁与 stagnation 按 [verification-redaction.md](verification-redaction.md)。
- CLI 真实行为不变（v4 CLI 从未配置 backend）。
- 退出：authority 矩阵；生产代码无 backend 符号；文档更新。

### P6 — 密封脱敏边界

- 所有写路径使用 sealed 类型；删除装饰器与裸写入口；CLI 不再传裸 store。
- 退出：植入 secret 的 E2E 零泄漏；二进制证据拒绝；arch 测试禁止裸写签名。

### P7 — Runtime/CLI/API 清理

- 新增 `runtime`：配置、注册表、lease、信号、装配；`internal/cli` 只依赖 runtime 与只读接口。
- 删除根 `harness.go` 与 `engine/core_aliases.go`；适配器保留现有路径。
- `signal.NotifyContext`；`defer Runner.Close()`；只读命令 `OpenReader`；Windows lease。
- 退出：CLI golden、嵌套 typo、信号、只读无 lease、Windows build、依赖/别名 arch 测试。

### P8 — 收尾加固

- Docker：capability allowlist、拒绝 `network:host`/container 网络模式、挂载目标校验、compose override 清理、agent 出网默认限制。
- 修正 `examples/verify.sh` 使用 `evidence_dir`；`verify.Command` 上限与显式环境。
- 严格时间解码；`schema_meta` 单行；fuzz 语料与 CI Windows。
- 退出：安全表测试、Docker 集成（实际运行才声称通过）、fuzz ≥30s。

### P9 — 切换与删除 v4

- 默认 storage 切到 v5；config `harness/v5`；v5 拒绝 v8。
- 删除 v4 桥/store/旧类型/旧测试；更新根文档到 schema 5/Manifest v5。
- release workflow 确保 windows/amd64 可构建；发布标签策略在 P9 前单独决定（[ADR 0006](adr/0006-release-versioning-deferred.md)）。
- 退出：cutover 与回滚演练；全量矩阵；生产库 v5；审计为空；冒烟 Run 仅通过 verifier 成功。

## 5. 切换 Runbook

### 5.1 前置条件

- 所有 v4 写者停止；确认 `<db>.lock` 无持有者；`runs list` 无非终态 Run（或已显式中断）。
- 准备足够的磁盘空间放置 v8 备份、归档与 v5 新库。
- 保留 v4 二进制与已验证备份，用于回滚窗口。
- 采用双人复核：一人执行，一人核对 manifest 与 digests。

### 5.2 步骤

1. **冻结**：停止调度与所有 CLI 写命令。
2. **备份**：用 v4 二进制执行 `harness db backup --db live.db --out v8-backup.db --manifest v8-backup.json`。
3. **独立验证**：另一进程执行 `harness db verify-backup --db v8-backup.db --manifest v8-backup.json`；校验 SHA-256、大小、schema 8、行数、事件头、artifact 字节总数。
4. **异地保存**：复制备份到不可变存储；记录 digest。
5. **归档旧库**：把 `live.db`、`live.db-wal`、`live.db-shm` 移动到 `archive/v8-<timestamp>/`（移动，不删除）。
6. **创建 v5**：`harness db cutover --db live.db --manifest v8-backup.json --archive archive/v8-<timestamp>`；工具重新验证 manifest，获取写租约，在同目录临时文件创建 v5 库，fsync，原子 rename，写 `cutover.json`（两侧 digest、时间、操作者）。
7. **自检**：`harness doctor --db live.db`；确认 schema 5、历史为空、链可验、lease 可获取/释放。
8. **冒烟**：执行一个已知目标与 verifier 的 Run；确认只能经 verifier 成功；`db verify-chain` 通过；检查 journal/读模型一致。
9. **观察窗口**：保留 v4 与备份；监控错误与审计。

### 5.3 回滚

1. 停止 v5 写者；确认 lease 释放。
2. 把 v5 库与 sidecar 移动到 `archive/v5-<timestamp>/`（保留，不删除）。
3. 从已验证的 v8 备份恢复 `live.db`；恢复 WAL/SHM（若备份时存在）；校验 digest。
4. 启动 v4 二进制；`doctor`/事件链校验；确认可读。
5. **数据边界**：cutover 之后产生的 v5 Run 不能导入 v4；它们保留在 v5 归档中，需单独审计。回滚窗口与保留策略必须在执行前书面确认。

### 5.4 失败处理

- 备份/验证任一失败：停止，不进入 cutover。
- cutover 临时库创建失败：保留 v8 归档，重新执行；不得在原库上"修复"。
- rename 后自检失败：按回滚步骤恢复 v8；保留 v5 临时库供分析。
- 冒烟失败：区分 verifier 配置问题与 v5 缺陷；不得跳过 verifier 或手工写终态。

## 6. 测试与门禁矩阵

| 阶段 | 必跑 |
|---|---|
| P0 | `git diff --check`；文档链接；评审 |
| P1 | `go test ./...`、`-race`、`vet`、`build`；arch 纯度 |
| P2 | 上述 + 状态表、`FuzzAdvance`、digest golden |
| P3 | 上述 + porttest、故障注入、篡改链、cutover fixture |
| P4 | 上述 + 同一 kernel 双存储、kill-restart、并发 |
| P5 | 上述 + authority 矩阵、符号 grep |
| P6 | 上述 + secret E2E、`FuzzRedactor`、arch 裸写检查 |
| P7 | 上述 + CLI golden、配置严格性、信号、Windows build |
| P8 | 上述 + Docker 安全表、fuzz 30s、`HARNESS_DOCKER_TESTS=1`（实际运行） |
| P9 | 全量 + Windows + 集成 + cutover/回滚演练 |

固定门禁：

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/harness
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...
```

## 7. 风险

| 风险 | 影响 | 缓解 |
|---|---|---|
| 分阶段直接进 main 导致过渡层沉淀 | 双实现腐化 | 兼容层清单 + 每 PR 标注 + P9 删除门禁 |
| v4 存储桥语义偏差 | main 行为回归 | 同一 porttest 跑桥与 v5 store；端到端对照 |
| 事件溯源回放错误/慢 | 审计/恢复不可信 | golden replay、fuzz、checkpoint 回退、`verify-chain`、基准 |
| cutover 丢历史 | 不可逆数据丢失 | 独立 manifest 验证、归档不删除、原子 rename、双人复核、回滚演练 |
| 脱敏 新旁路 | 泄密 | sealed 类型、单一写端口、arch 测试、E2E 植入扫描 |
| resume 与中断竞态 | 数据损坏 | 排他 lease、active map、expected-head CAS、并发测试 |
| Windows lease/构建 | 发布失败 | `x/sys/windows.LockFileEx` + build tags + CI Windows |
| panic 捕获掩盖 bug | 可靠性 | 只在 goroutine 根 recover；持久化 failed；堆栈脱敏；drain/leak 测试 |
| 模块路径/发布版本未定 | 发布机制 | ADR 0006：路径暂不变；P9 前单独决策 |

## 8. 完成定义（P9）

1. 默认运行与存储为 v5；schema 5；`harness/v5` 配置。
2. 生产代码无 `CompletionSource`/`Backend`/v4 桥/旧 core 类型/根 facade/别名层/裸写入口。
3. 根 README/AGENTS/CLAUDE 与示例反映 v5（仅 verifier、schema 5、resume 正确、Windows 构建通过）。
4. 全量门禁 + Windows + 集成通过；cutover 与回滚已演练并记录。
5. 生产数据库为 v5；审计为空且链可验；冒烟 Run 仅通过 verifier 成功。
6. 回滚窗口、归档位置与数据边界已书面确认。
