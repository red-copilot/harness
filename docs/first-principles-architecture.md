# Red Harness 第一性原理架构研究

- 日期：2026-10-28
- 状态：架构研究，结论已选定并已实施
- 研究方式：基于本仓库代码、测试与设计的静态证据
- 历史发布基线：`main@0bbee5e`
- v2 实现底稿：`5bbddb9`
- 当前实现：Claim/Intent v3（schema v7）
- 读者：Red Harness 维护者与架构师

> **v3 update (2026-10-28):** The current implementation is Claim/Intent v3 with one Executor, strict `report.json`, SQLite schema v7 containing artifact bytes, and generated OpenAPI contracts. See `docs/superpowers/specs/2026-10-28-first-principles-harness-v3-design.md`. The v2 comparison below is retained as historical design evidence.

> **结论先行：** Claim/Intent v3 是唯一控制架构，并已一次性切换。`main@0bbee5e` 的单 Agent、`ExplorationState` 与 Frontier steering 已退役；Branch 树和 writable Knowledge 不保留为第二控制语义。旧 Branch/Knowledge 数据只保存在切换前的验证备份中。

> 本文比较的是架构，不宣称任一实现已经通过完整 Go、race、Web、Docker 或 live TSec/VPN 验证。下文列出的测试是静态阅读到的代码证据，并非本次运行结果。

## 1. 执行摘要

Red Harness 的本质不是替模型思考，而是为一个不可信 Agent 建立可机械检查的执行与信任边界：调用方授权一个逻辑 Target，系统在其上允许自主探索，同时由代码约束资源、证据来源、控制状态、审计和成功判定。

从这个本质出发，架构比较不能以“哪个探索拓扑更聪明”作首要标准，而应先问：

1. 模型、工具输出和 Agent 自述能否伪造成功？
2. 一个语义结论能否绕过证据链进入后续推理？
3. 多个并发执行者能否在没有显式提交点的情况下改写控制状态？
4. 审计能否为了升级、清理或重试而被删除或覆盖？
5. 模型上下文是否有确定性上界？
6. 已知秘密能否进入 canonical persistence？
7. 任一持久化、解析、取消或清理失败是否必然导致非成功终态？
8. 授权 Target 身份能否被运行实例替换？

这些问题构成硬门禁。硬门禁不通过，路线即淘汰，不用加权分数掩盖；通过硬门禁以后，再比较控制清晰度、可测试性、状态可达性、失败恢复、扩展成本和迁移风险。

三条路线的主要判断如下：

| 路线 | 代表证据 | 信任与控制的根本问题 | 结论 |
|---|---|---|---|
| 单 Agent + Frontier steering | `main@0bbee5e`；`state.go`、`frontier.go`、`runner.go` | 一个长上下文同时承载语义状态与噪声；“投影”不能天然提供跨上下文的事实边界；生产路径没有 v2 的证据先于 Fact 机制 | 保留为已发布基线，不再演进 |
| Branch v1/v2 | `717a05a`、`adad229` 等提交，以及已提交的 expansion/收敛设计与计划 | 能把并行分支和父子约束机械化，但“分支”容易把执行拓扑误当作认知事实；最终仍需要从执行结果中提炼可复用事实 | 可视为通往 Claim/Intent 的中间态，不作为目标架构 |
| Claim/Intent v3 | `5bbddb9`；`core/`、`engine/`、schema v7 | 把“执行结束”与“证明了什么”分开；Fact 有 Observation/Artifact 来源；单写者按 revision 提交；成功仍只由 Verifier/backend 产生 | **选定目标架构，立即切换** |

v2 的胜出不是因为“单槽”或“blackboard”这个名称，而是因为它把不可信输入与 canonical 状态之间的转换变成了显式、可测试的协议。其核心价值是：

- 模型只提交 Claim/Intent 提议；ID、状态转换、容量和授权由代码生成或校验。
- Finding Fact 必须引用当前 Attempt 的 Observation；Observation 必须引用已归档 Artifact。
- 执行者只返回不可变结果；`engine` 循环是 board 状态的唯一写者。
- `CanonicalWriter.Commit` 与 `Finalize` 把 board revision、规范化记录、历史和事件放进原子边界。
- `RunPhase` 与 `RunOutcome` 分离，成功需要成功 verdict；失败路径不会被“看起来完成”覆盖。
- 切换前先验证 SQLite 备份，再创建全新 v2 数据库；旧历史保留在备份中。

v2 也有明确的未解成本：固定一个 Executor 是产品选择，不是自然定律；Reason/Explore/Conclude 协议复杂度上升；Reasoner 失败上限可能降低探索覆盖率；没有 checkpoint/resume；TSec `BENCHMARK_TOKEN` 仍是容器信任边界内的产品例外。静态研究不能证明这些取舍在真实 TSec 题目上总体更优。

## 2. 证据层级与范围

### 2.1 证据优先级

本文按以下顺序采信证据：

1. 可编译代码及其直接测试。
2. 已提交的契约文档与迁移文档。
3. 提交历史中可核对的设计演进。
4. 未提交的设计草稿，只作为历史审计线索，不作为目标或现状的权威来源。

发布基线固定为：

```text
0bbee5e7d3baadf500af2ff1f7d73dd1ae8ea482  docs(plan): add exploration expansion implementation plan
```

最新 v2 证据固定为：

```text
5bbddb93a1d23dcdf70a3f59ed43522e2da9024f  refactor(core): converge on v2 fact intent architecture
```

`5bbddb9` 不在当前 `main` 的祖先链上，也不能仅凭文档标题视为已发布。本文把它作为可达的本地实现证据：用于推导目标架构、识别代码强制点和剩余风险，不把它写成 `main` 的现状。

### 2.2 事实、约束与参数

架构讨论中必须区分三种陈述：

- **不可约约束**：如果违反，系统就不再满足其安全/信任目的。例如“模型输出不是成功”。
- **派生不变量**：由约束和设计选择推出、可在代码中局部执行的规则。例如“Finding Fact 必须引用 Observation”。
- **产品参数**：可变化且有成本的配置。例如“恰好一个 Executor”、“最多 20 个 Intent”、“board context 64 KiB”。

`2 个 Executor`、`20 个 Intent`、`64 KiB`、`10 分钟 Attempt` 都是产品参数，不是第一性原理。把它们称为公理会制造虚假的必然性，掩盖吞吐、覆盖率和成本的取舍。

### 2.3 范围

**包含：** 本仓库的信任边界、控制状态、证据与 Fact 的关系、权威裁决、持久化、迁移方向和三路线比较。

**不包含：** 行业产品横向调研；代码迁移；完整 Go/Web 验证矩阵；Docker、live TSec/VPN 或真实题目成功率证明。

## 3. 从不可约问题推导约束

给定一个会执行 Shell、可能输出恶意结构化数据、且无法被信任为“知道自己成功”的 Agent；一个调用方授权的单一逻辑 Target；有限的时间、token、工具、并发和存储预算；一个只能由外部证据裁决的任务；以及必须保留且不能泄漏已知秘密的审计记录，系统必须同时满足：

1. 允许 Agent 自主选择调查路径和使用工具。
2. 机械限制路径、并发、重试、上下文和资源。
3. 不允许 Agent 文本、工具退出码或自报状态建立成功。
4. 允许后续模型依赖的语义结论追溯至本次执行产生的 Artifact。
5. 任一 canonical 写入失败优先于语义上的“完成”。
6. 运行实例不能改变调用方授权的逻辑 Target。
7. 审计可复盘且迁移不能销毁既有事件/日志。

由这些要求可以推导以下不可约约束：

| 编号 | 不可约约束 | 架构含义 |
|---|---|---|
| C1 | 模型输出是提议，不是事实 | 解析、校验、ID 生成和状态转换必须由代码执行 |
| C2 | 工具退出码只说明进程结果 | 它不能直接变成 Observation、Fact 或 Verdict |
| C3 | 证据先于 Fact，Fact 先于后续推理 | 保存链路必须可回溯，且使用当前 Attempt 的合法证据 |
| C4 | 一个 Run 的控制状态只有一个写者 | 并发执行者只返回结果；状态转换在单一控制循环提交 |
| C5 | 审计是证据，不是可重建缓存 | canonical 记录只追加；旧 schema 数据不能为升级而删除 |
| C6 | 模型上下文必须有确定性上界 | 条数、字节、排序和截断都要可审计 |
| C7 | 已知秘密不能进入 canonical persistence | 脱敏必须位于统一写入边界，而不是散落在调用点 |
| C8 | 失败必须关闭 | 解析、存储、取消、清理和权威错误都映射为非成功终态 |
| C9 | 授权身份与运行绑定分离 | 容器名、IP、端口或 challenge 实例不能替换 Target ID |
| C10 | 只有 Verifier 或 backend terminal completion 能建立成功 | 所有其他“完成”信号只能是观测或触发验证 |

这些约束并不要求固定使用 Claim/Intent。它们只要求同一组可验证性质。Claim/Intent v3 是一种满足方式；Branch v2 也可能满足部分要求，但必须通过同样的硬门禁才能进入目标架构。

## 4. v2 约束到代码强制点

本节把 v2 的声明映射到代码与静态测试。代码路径以 `5bbddb9` 为证据；“测试证据”是仓内测试的存在和意图，不代表本次执行通过。

| 约束 | 推导与失败模式 | 唯一或主要强制点 | 静态测试证据 | 未覆盖风险 |
|---|---|---|---|---|
| C1 模型输出是提议 | 若模型可生成 Fact ID、origin/goal 或直接状态，恶意 JSON 就能污染后续上下文 | `core.ValidateFact`、`ValidateIntent`、`ValidateExploreReport`、`ValidateReasonReport`；`engine.boardState.addFact/createIntent`；Prompt 与 report 只接受结构化提议 | `TestReasonReportCannotExceedFreeSlots`、`TestReasonReportRejectsIntentsAndGoalCandidateTogether`、`TestExploreReportOnlyProducesFindingOrNegative`、`TestBootstrapReportCannotCreateChildIntentsOrGoal` | 解析器接受嵌入 JSON/占位符，模型仍可能用语义措辞影响提议质量；这不等于身份可伪造 |
| C2 工具退出码不是成功 | shell 成功可能只是命令跑完，不能证明目标状态 | `engine.RunAgentTurn` 只产生 TurnResult；authoritative decision 在 `runController.finishExecution` 与 backend/Verifier 路径 | `authority_test.go` 的 backend/verifier 场景；`blackboard_engine_test.go` 的尝试与证据测试 | 个别事件/日志可能只保留退出结果；代码静态阅读不能证明所有 runtime 会把失败完整上报 |
| C3 证据先于 Fact | 没有 Artifact 的“发现”会变成不可复核的记忆 | `ValidateFact` 的 Finding 规则；`ArchiveAttemptEvidence`；`blackboardEngine.handleAttempt` 先归档 Observation/Artifact，再 `addFact` | `TestFindingFactRequiresObservation`、`TestBoardRejectsFindingWithoutEvidence`、`TestArchiveEvidenceRequiresObservationFile`、`TestBlackboardEngineStopsOnEvidenceValidationFailure` | 证据路径的竞争条件和 artifact store 故障主要靠单元测试；真实容器/写盘失败未在本研究运行 |
| C4 单控制写者 | 两个 slot 同时直接改 board 会造成丢更新、重复 claim 或关闭后写入 | `engine.blackboardEngine.run` 单 select loop；`boardState` 非并发安全；worker 只送 `boardAttemptResult`/`boardReasonResult`；`runController.apply` 串行化控制迁移 | `TestControllerIsSingleWriterUnderConcurrentCompletion`、`TestControllerSerializesControlTransitionsThroughApply`、`TestBoardRejectsIntentClaimOnOccupiedSlot`、`TestReasonAdmissionNeverExceedsTwoClaimed` | 只能静态确认主循环结构；没有本次 race 运行；后台 watcher 与主循环的接口仍需按实际实现审查 |
| C5 审计只追加 | 为迁移清库或覆盖历史会让旧运行不可复盘，且会隐藏失败证据 | schema migration 的 additive/backfill 路径；`CanonicalWriter.Commit` 追加 events、Intent/Attempt version；`Finalize` 原子终结；旧 Branch/Knowledge 只读 | `TestMigrationPreservesAuditRowsAtV6`、`TestCommitRoundTripsAndAppendsIntentVersions`、`TestFinalizeIsAtomicAndTerminalWritesAreRejected` | 迁移的兼容性只由命中 fixture 覆盖；实际历史数据库形态可能更多；文档不能替代数据库备份 |
| C6 上下文有界 | 无界 Fact/Hint/Assistant 内容会造成注意力稀释和提示注入放大 | `ContextPolicy`、`boundedFactsWithPolicy`、`boundedHints`、`truncateUTF8`、`RunAgentTurn` 上限 | `TestReasonContextHonorsPolicyByteAndItemBounds`、`TestReasonPromptIncludesOnlyBoundedGraph`、`TestReadEvidenceFileEnforcesActualByteLimit` | 上界是产品参数，不是性能保证；如果模型需要更多证据，截断会降低效果 |
| C7 秘密不落盘 | 事件、日志、Spec、Result、Verdict 或 binary artifact 中的秘密无法可靠撤回 | `engine.redactingStore` 统一包装 `CreateRun`、`UpdateRun`、`AppendEvent`、`AppendLog`、`Commit`、`Finalize`；Artifact 文本先脱敏、二进制命中拒绝 | `TestRedactingStoreCoversBoardCommitAndFinalization`、`TestTextArtifactIsRedactedBeforeHash`、`TestBinaryArtifactWithKnownSecretIsRejected`、`TestRedactorHandlesEveryRegisteredSecretLength` | TSec token 被显式传入容器供 submission skill 使用；脱敏不阻止容器内代码读取它。容器仍是凭据信任边界 |
| C8 失败关闭 | 解析失败、写失败或清理失败若被当作“无发现”，会把不确定状态伪装成成功 | `runController.finishExecution` 要求成功有成功 verdict；`blackboardEngine` 返回错误；store terminal 写约束；cleanup failure downgrades success | `TestSessionCleanupFailureDowngradesSuccess`、`TestTargetCleanupFailureDowngradesSuccess`、`TestInFlightCanonicalEventFailureDowngradesSuccess`、`TestBlackboardEngineStopsOnEvidenceValidationFailure` | 需要真实故障注入才能覆盖所有 I/O 交接；本次未运行 race/故障集成测试 |
| C9 授权身份与绑定分离 | provisioner 若可换 Target ID，模型或容器错误就能让系统探测未授权目标 | `TargetSpec` 与 `TargetBinding` 分离；`ValidateTargetBinding`；`AuthorizeTarget`；Evidence target 校验 | `TestTargetFingerprintUsesStableSpecIdentity`、`TestTargetBindingMustPreserveAuthorizedIdentity`、`TestAuthorizeTargetUsesEffectiveBinding`、`TestArchiveEvidenceRejectsUnauthorizedObservationTarget` | 这是语义引用检查，不是网络 ACL；调用方仍负责 egress |
| C10 权威裁决 | Fact、Reason、goal candidate 或 agent settled 若可成功，安全结论可被伪造 | `engine.CompletionSource`、`Verifier`、`runController.finishExecution`；Fact/Reason 只能触发验证 | `TestBackendCompletionDuringVerifierWins`、`TestBackendWatcherStopsBeforeRunReturns`、`blackboard_engine_test.go` 成功路径 | 静态测试存在不等于完整集成通过；Verifier/backend 的真实配置和 TSec completion 仍需运行验证 |

### 4.1 为什么 v2 的结构优于“把约束写进 prompt”

prompt 可以建议模型不要伪造 Fact，却不能阻止模型输出一个自认为合法的对象。v2 的价值在于每条关键约束都有代码失败点：

- 没有 Observation 的 Finding 会在 board 校验失败，而不是生成“低信任知识”。
- 越界授权 Target 会在 evidence archive 前失败，而不是进入后续上下文。
- 旧事件不会因为 schema 升级被删除，而是由 migration 保留。
- 事件写入失败会使 Run 非成功，而不是静默吞掉审计缺口。
- backend 完成与 Verifier 竞争时有明确仲裁顺序，而不是由 goroutine 调度顺序决定结果。

这也是把“不可约约束”和“产品参数”分开的原因：前者决定系统是否值得信任，后者决定它在某一资源预算下的行为。

## 5. 三条路线比较

### 5.1 `main@0bbee5e`：单 Agent + 状态投影 + Frontier steering

证据：

- `types.go` 的 `RunSpec.Agents`、`AgentSession`、`RunSpec.Verifier`。
- `state.go` 的 `ExplorationState`、`ReduceExploration`。
- `frontier.go` 的 `ProjectExplorationFrontier`、`BuildExplorationContext`。
- `runner.go` 在 `turn_end` 后记录 `exploration.state.updated`、`exploration.frontier.updated`，并以 `Steer` 注入 focus。

优点：

- 控制面简单，现有实现与测试直接可见。
- 单个长会话便于模型保持任务上下文。
- Frontier 是事件的确定性投影，不额外持久化可变 frontier。
- 工具输出明确标为 untrusted observation，`agent_settled` 才有 session 完成语义。

硬门禁问题：

- 一个模型的上下文同时承载语义判断和原始工具输出，难以从结构上证明“证据先于 Fact”。
- 没有独立的 Claim/Intent 归一化层；“已知事实”和“下一步探索”主要靠 prompt 与投影维持。
- 并发与探索宽度没有 v2 的显式 slot/claim 协议，不能从当前代码推出同样的可复现行为。
- 对已完成事实的追溯、跨 session 的来源和 board revision 没有 v2 那样直接的强制点。
- 不能把 `ExplorationState` 称为 canonical 事实源；它是控制投影，不能替代 Verifier。

结论：适合作为发布基线与兼容参考，不应继续叠加新的探索语义。继续扩展它会把“第一性原理”变成 prompt 文案，而不是代码不变量。

### 5.2 Branch v1/v2：Planner + Executor 的假设树

证据：

- 已提交的 `docs/superpowers/specs/2026-10-21-exploration-expansion-design.md`：单 Controller + 两个短期 Explorer、Frontier、Fact/Ledger、结构化 ExplorerReport。
- `717a05a feat(exploration): replace frontier engine with binary branch tree`。
- `adad229 feat(exploration): enforce binary branch expansion in code`。
- 已提交的收敛计划与设计指出：若“二叉”只存在 prompt，或 `MaxActiveBranches` 实际串行，则声明不可信；必须让机械不变量由 Go 强制。

优点：

- 比单会话有更明确的探索拓扑和并发模型。
- 可以把子分支数量、深度、节点数和 active attempts 都做成机械限制。
- 结构化 attempt report 可以把“本轮结果”与“下一步”分离。
- 能让系统比较不同假设路径，适合作为从长上下文走向事实图的中间阶段。

硬门禁问题：

- “分支”是执行/搜索拓扑，不自动等于可复用的客观事实。一个分支 `local_success` 仍然是观测，不应成为成功。
- 收敛文档已指出：如果 `MaxChildrenPerBranch`、`MaxActiveBranches` 等字段只在 prompt 里，系统会产生虚假的保证；v2 必须以代码和测试兜住这些字段。
- Knowledge trust/reuse/conflict 状态如果没有可达转换，会造成比缺少状态更危险的假象。
- 即便 Branch 通过了 C1/C3，仍可能保留“树节点是主要认知单元”的偏差，后续要从树节点再抽 Fact。
- 它没有解决“哪些事实能进入后续推理”的统一证据边界，除非额外实现 Fact/Ledger 和 provenance。

结论：Branch v2 是有价值的历史中间态，适合证明并发/展开约束可以机械化，但不是最终目标。若作为长期主线，会迫使系统同时维护树语义和事实语义，增加双控制路径风险。

### 5.3 `5bbddb9` Claim/Intent v3：目标架构

证据：

- `core/`：`RunDescriptor`、`RunPhase/RunOutcome`、`RunPolicy`、`Fact`、`Intent`、`Hint`、`Observation`、`Artifact`、`Verdict`、typed events。
- `engine/ports.go`：`CanonicalWriter.Commit/Finalize`、`TargetProvisioner`、`CompletionSource`、`Verifier`、`ArtifactStore`。
- `engine/blackboard.go` 和 `engine/blackboard_engine.go`：两槽 board、Bootstrap、Reason、Explore、Result 收敛。
- `store/sqlite`：schema v7、board commit、Intent/Attempt version、原子 Finalize。
- 设计文档 `docs/superpowers/specs/2026-10-28-first-principles-harness-v2-design.md` 标为 Implemented，但本文仍以代码与测试为实证据。

关键推导：

1. **执行结束 ≠ 语义完成。** `IntentAttemptPhase` 描述 running/concluded/failed/cancelled/timed_out；Fact 描述 finding/negative。两者分离后，超时或工具结束不能被直接解释为“成功”。
2. **Reasoner 是提议者，不是事实作者。** Reason report 只能提出 Intent 或 goal candidate；ID、容量、claim、conclusion 和来源 Fact 由 board 校验。
3. **证据链是单向的。** `evidence file → Artifact → Observation → Fact`。Finding Fact 必须引用 Observation，Observation 必须引用当前 Attempt 的 Artifact。
4. **并发是 slot 资源，不是树深度。** 一个固定 Executor slot 各自最多一个 active Attempt；完成一个即可补位，不需要 round barrier。
5. **单写者让 revision 有意义。** `boardState` 只在 engine loop 中变更；`Commit` 携带 revision，规范化记录、历史版本和事件同时提交。
6. **成功仍然不属于 board。** `goal_candidate` 只触发 Verifier；backend terminal completion 是外部权威；`finishExecution` 再次要求成功 verdict。

为什么它不是“又一个框架”：

- 它没有抽象通用工具协议或模型路由。
- 它把 adapter 放在外面：Pi、Docker、TSec、Verifier、SQLite 实现端口，不把领域语义塞进核心。
- 它把 SQLite 定位为 canonical audit，而不是可随意重建的 cache。
- 它不声称网络 ACL；`caller_managed` 明确留给调用方。

结论：Claim/Intent v3 是明确推荐的目标架构。它不是以“单槽”作为第一性原理，而是以可验证的提议、证据来源、单写者和权威裁决作为第一性原理；单槽和 blackboard 只是满足这些约束的一组产品选择。

## 6. 目标架构

### 6.1 分层

```text
Caller / CLI / HTTP / Web
            │
            ▼
      core contracts
  Task, TargetSpec, Policy, Events
            │
            ▼
     engine control kernel
  Runner / RunController / board / slots
            │
   ┌────────┼──────────┬─────────────┐
   ▼        ▼          ▼             ▼
 Pi       Docker     TSec        Verifier
 runtime  sandbox    authority   verifier
   │        │          │             │
   └────────┴──────┬───┴─────────────┘
                  ▼
            SQLite v6
     canonical audit + board history
```

- `core`：纯领域契约、机械 validator、事件 schema；只依赖标准库。
- `engine`：生命周期、单写者 board、slot 调度、预算、取消、脱敏和权威仲裁。
- `harness` 根包：薄 façade，不拥有状态机或 I/O。
- adapters：`pi`、`sandbox/docker`、`tsec`、`verify`、`store/sqlite`；实现端口，不能导入 UI/CLI。
- surfaces：`server`、`internal/cli`、`webui`、`web`；只消费同一套 engine 与读模型。

### 6.2 目标公开契约

目标契约以 v2 为准：

- `core.RunDescriptor`：`SchemaVersion=3`、`Task`、单一 `TargetSpec`、`Reasoner`、恰好两个 `Executors`、`RunPolicy`。
- `engine.RunSpec`：在 descriptor 之上绑定 runtime、backend、Verifier、artifact store 和 workspace。
- `RunPhase`：`queued → provisioning → running → finalizing → verifying → terminal`。
- `RunOutcome`：`succeeded | failed | cancelled | timed_out | stagnated | interrupted`。
- board：`Fact`、`Intent`、`Hint`、`IntentAttempt`、`Observation`、`Artifact`。
- 权威：`CompletionSource`、`Verifier`、`Verdict`。
- 持久化：`CanonicalWriter.Commit` 与 `CanonicalWriter.Finalize`。

### 6.3 信任边界

- Agent workspace、stdout/stderr、tool output、Report/JSON、Fact 提议和 Observation 提议都不可信。
- `Reasoner` 只能提议 Intent；其输出不能直接写 board。
- `Executor` 只能执行一个已 claim 的 Intent，并返回结构化报告；不能创建子 Intent。
- 只有 engine loop 能改变 board；只有 `CanonicalWriter` 能持久化 board 和 terminal state。
- Finding Fact 必须沿 `Artifact → Observation → Fact` 回溯，并绑定当前 Attempt。
- TargetSpec 是授权身份；TargetBinding 只是运行实例。网络隔离不由 SDK 声称。
- 成功只能来自 `Verifier` 成功 verdict 或 backend terminal completion。

### 6.4 失败模型

| 场景 | 目标行为 |
|---|---|
| malformed Reason/Explore JSON | 解析/校验失败；不生成伪 Fact；按 policy 重试、abandon 或 fail closed |
| Finding Fact 缺少 Observation | 拒绝创建 Fact；Attempt 失败或不被视为有进展 |
| Evidence 路径越界、symlink、目录、超限 | 在 archive 前拒绝，不写 Artifact metadata |
| canonical store 写失败 | Run 非成功；不得继续消费结果或生成成功结果 |
| session/target cleanup 失败 | 成功候选降级为 failed，保留审计 |
| 取消或墙钟到期 | 非成功 terminal outcome；清理后才能关闭 |
| 没有 backend/Verifier 权威 | 不得成功 |
| 旧 schema / 新 schema / 未知事件 | 旧 schema additive 迁移并保留记录；更新版本 fail closed；未知 UI 事件局部忽略 |
| 秘密检测 | 文本脱敏；二进制命中拒绝；已知 token 不进入 SQLite/Result/Verdict/诊断 |

## 7. 迁移决策

### 7.1 一次性切换

决策：**立即切换 Claim/Intent v3。**

- v2 成为唯一可写控制语义。
- `main@0bbee5e` 的 `ExplorationState`/Frontier 不再扩展，不进入新运行路径。
- `Planner` 改名为 `Reasoner`，不再产生子节点。
- 恰好一个 Executors；不再有 writable Branch/Knowledge 控制路径。
- 旧 Branch/Knowledge 数据不作为新的可写控制路径；生产切换后仅保存在验证备份中。
- 不保留双写、双 CLI 配置或长期兼容层；迁移应是可审计的断代，而不是长期双路径。

### 7.2 数据库和回滚

- 生产切换采用备份后重建：先验证备份的 SHA-256、大小和关键表行数，再创建全新 schema v7 数据库。
- 旧 Run/Event/Branch/Knowledge 不复制到新可写库；其审计留存在验证过的备份中。
- schema v7 由 v2 二进制创建；`OpenFresh` 拒绝覆盖已有数据库或 WAL/SHM。
- 数据库升级视为单向边界：旧二进制回滚必须先恢复备份，不能继续写新 schema。
- 新库初始化失败、备份校验失败或不支持的新 version 必须 fail closed。

### 7.3 迁移顺序

1. 冻结旧控制语义；停止新增 Branch/Knowledge 写者。
2. 准备 v2 descriptor、`harness/v3` 配置和一个 Executor。
3. 备份并验证 SQLite；记录 SHA-256、大小与关键表行数，确认备份完整。
4. 以 `OpenFresh` 创建全新 v2 库并启用 Reasoner/RunController；旧历史只从备份恢复。
5. 观察 Reasoner 失败、slot 补位、证据归档、cleanup 和 Verifier/backend 竞争。
6. 删除旧写入入口、旧 CLI 写路径和旧控制代码；不以“停止调用”代替删除第二写者。
7. 待真实 TSec 冒烟与统计确认后，再评估并发参数和上下文预算。

## 8. 风险登记

| 风险 | 影响 | 处置 |
|---|---|---|
| 单槽固定并发 | 任务可能需要更多并行假设；两槽可能限制吞吐 | 视为产品参数；先测量真实任务，再决定是否扩展；不要把它写成第一性原理 |
| Plan/Execute/Conclude 协议复杂 | 多阶段状态和超时增加实现/理解成本 | 用 Attempt phase、单一 engine loop 和明确 timeout 约束；保持失败关闭 |
| Reasoner 失败上限 | 可能过早停止探索，降低覆盖率 | 记录失败事件，基于真实基线调参；不能以放宽成功权威换取覆盖率 |
| 上下文截断 | 有界上下文会丢失长尾证据，影响模型判断 | 保留 origin/goal 与确定性排序；记录 truncation；评估 artifact 可访问性 |
| Evidence archive 故障 | 不能生成 Finding Fact，可能降低完成率 | ArtifactStoreError 直接终止；不把缺证据当成负事实 |
| TSec token | 容器可读取产品例外凭据，脱敏不能阻止读取 | 明确容器在凭据信任边界内；限制 mounts、能力、网络和 secret passthrough |
| SQLite 单向升级 | 旧二进制回滚可能无法继续写入 | 切换前验证备份；回滚恢复备份；新库不复制旧 Run |
| 静态验证不足 | 本文发现的是代码级强制点，不证明 Docker/TSec/race 全通过 | 迁移验收阶段运行完整矩阵；Docker/live 检查只有在实际执行后才可声称通过 |
| 模型语义质量 | 结构合法不代表推理正确；Fact 仍可能语义错误 | 只把 Fact 当后续提案的来源，不让其成为成功；用 Verifier/backend 裁定 |
| Web/事件兼容 | 新 board 与旧 Branch 同时可读，展示可能混淆 | 旧事件只读 fallback；新 Run 只写 Claim/Intent 事件；未知事件局部忽略 |

## 9. 验收结论与后续

架构研究的验收标准：

- 每个不可约约束都能追溯到代码强制点或明确风险，而不是仅存在于 prompt/文档。
- 产品参数与第一性原理分开列出。
- 三条路线都有代表 commit/文件和明确淘汰理由。
- 当前架构说明单一控制写者、证据链、权威裁决和失败模型。
- 迁移说明一次性切换、验证备份后重建、旧数据留档与不可回滚边界。
- 不把静态研究写成运行测试结论。

结论：

1. **Claim/Intent v3 是唯一控制架构，且已成为当前实现。**
2. **`main@0bbee5e` 是发布基线，不是目标实现。**
3. **Branch v1/v2 是重要中间态，但不是第二生产控制路径。**
4. **已切换；旧 Branch/Knowledge 仅保留在验证备份中，不进入新可写库。**
5. **完整 Go/race/Web/Docker/live TSec 验证必须在实施阶段实际运行后才能报告通过。**

本文保留架构研究与路线比较；具体实现、schema v7、当前测试结果和迁移操作以代码、`docs/architecture.md` 与 `docs/migration-v2.md` 为准。
