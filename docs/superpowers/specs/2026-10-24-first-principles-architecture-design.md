# Red Harness 第一性原理架构设计（目标态）

- 日期：2026-10-24
- 状态：Proposed
- 基线：`feature/exploration-expansion`，当前 v1 实现（2026-10-24 本地 `go test ./...` 通过）
- 范围：全仓库架构设计，不包含实现改动
- 关系：保留 `2026-10-21-first-principles-offensive-agent-sdk-design.md` 中已经实现且仍然成立的不可约约束；本文件取代未落地的 `2026-10-21-exploration-convergence-design.md` 及其阶段计划中与本文冲突的部分
- 说明：本文中的“v2”指目标架构代际，不承诺具体发布版本号

---

## 1. 结论

Red Harness 的本质不是“让 Agent 更聪明”，而是：

> 在调用方明确授权的单一目标上，让不可信 Agent 进行尽可能有用的自主探索，同时由代码而不是模型输出来保证资源边界、证据来源、知识转换、审计完整性和最终成功判定。

目标架构采用**契约优先的分层重构**。它保留现有产品能力、有界假设树和 Pi/Docker/TSec/SQLite 适配器，但收敛五个根因：

1. **授权身份与运行实例分离。** Target 的稳定身份不能随容器名、IP、端口或挑战实例变化。
2. **控制状态只有一个所有者。** 每个 Run 的状态转换由单一 Run Controller 串行提交，Runner 和 Run Handle 不再各自持有可变的控制真相。
3. **执行状态与语义结论分离。** “Attempt 怎么结束”和“这次尝试证明了什么”是两个正交维度。
4. **证据先于知识，权威先于成功。** Artifact、Observation、Knowledge 和 Verdict 形成单向、可验证的因果链。
5. **所有持久化只有一个脱敏写入边界。** 事件、日志、证据、知识、结果和错误都必须经过同一个 canonical writer。

架构不追求事件溯源式的理论纯粹性。SQLite 中的事件、规范化记录和终结事务共同构成 canonical audit；目标是可恢复、可查询、可验证，而不是从事件重放所有业务状态。

---

## 2. 第一性原理

### 2.1 不可约问题

给定：

- 一个不可信、会执行 Shell、可能输出错误或恶意结构化数据的 Agent；
- 一个调用方授权的逻辑 Target；
- 有限的 token、工具、时间、并发和存储预算；
- 一个只能通过外部证据判断是否成功的任务；
- 一个必须可审计、可复盘、不能泄漏已知秘密的运行环境；

系统必须同时满足：

- Agent 能自主决定调查路径和工具使用；
- SDK 能机械限制路径数量、并发、深度、重试、上下文和资源；
- Agent 的自由文本、工具退出码和自报状态不能成为 Run 成功依据；
- 所有被后续模型依赖的“知识”都能追溯到当前 Attempt 的真实 Artifact；
- 任一 canonical 写入失败都必须优先于任何“看起来成功”的语义结论。

### 2.2 不可约事实

| 编号 | 事实 | 架构推论 |
|---|---|---|
| F1 | 模型输出是提议，不是事实 | 所有 Planner/Executor 输出都必须解析、校验并由代码转换 |
| F2 | 工具退出码只说明进程结果 | 退出码不能产生 Observation、Knowledge 或 Verdict 成功 |
| F3 | 并发会让隐式状态共享产生竞态 | 每个 Run 只能有一个控制状态写者 |
| F4 | 运行实例是易变的，逻辑授权身份是稳定的 | TargetSpec 与 TargetBinding 必须分离 |
| F5 | 没有来源的结论无法复核 | Knowledge 必须沿 Observation → Artifact 链回溯 |
| F6 | 被删除或改写的审计无法证明历史 | 事件、日志和知识版本只追加；SQLite 仍为 canonical store |
| F7 | 无界上下文会稀释注意力并放大注入风险 | 所有模型上下文必须有确定性条数、字节和排序 |
| F8 | 失败关闭只有覆盖所有路径才有意义 | 存储、解析、取消、清理和权威错误都映射为非成功终态 |
| F9 | 已知秘密一旦进入持久化就无法可靠撤回 | 脱敏必须位于 canonical 写入边界之前，而不是散落在调用点 |
| F10 | 网络隔离的承诺必须与执行事实一致 | v1 保持 `caller_managed`，SDK 不声称实施网络 ACL |

### 2.3 派生不变量

| 编号 | 不变量 | 主要强制点 |
|---|---|---|
| I1 | 成功只能来自 `CompletionAuthority` 的 `Done && Success` 或 `FinalVerifier` 的成功 verdict | Authority service + final state machine |
| I2 | Agent 文本、工具退出码、Observation、Knowledge 和分支结果都不能建立 Run 成功 | Authority service |
| I3 | 一个 Run 同时最多只有一个控制状态写者 | Run Controller |
| I4 | Target 的稳定身份只由调用方声明的逻辑身份决定 | `TargetSpec` / `TargetFingerprint` |
| I5 | Provisioner 只能产生运行绑定，不能替换授权身份 | `TargetProvisioner.Provision` 返回 `TargetBinding` |
| I6 | 所有被持久化的 Observation 必须引用有效 Artifact，Knowledge 必须引用当前 Attempt 的 Observation | Evidence Archiver + Knowledge Ledger |
| I7 | 只有代码能生成 Branch、Attempt、Observation、Artifact、Knowledge 和 Verdict 的身份 | Core |
| I8 | 所有 Event、Log、Artifact、Knowledge、Result 和 Error 在落盘前经过同一 Redactor | AuditWriter |
| I9 | canonical 写入失败使 Run 进入失败关闭路径，探索不得继续 | AuditWriter + Controller |
| I10 | 运行中未完成的 Run 在重启后只标记 `interrupted`，不恢复、不重放副作用 | Runner recovery |
| I11 | Planner 和 Executor 收到的上下文有确定上界，且拒绝已证伪知识 | Context Builder |
| I12 | 每个公开状态都必须可达，每个状态转换必须有目标测试 | Reachability suite |
| I13 | 事件 schema 带版本；读取端遇到未知事件必须跳过而不是失败整页 | Event contract + ReadStore |
| I14 | HTTP 控制面仅面向受信操作者，默认只监听本机 | CLI + HTTP boundary |
| I15 | 容器不得获得 Docker socket，必须保持只读根、`no-new-privileges`、能力最小化和资源限制 | Docker adapter |
| I16 | 复证来源的身份是 `(RunID, SessionID)`，重复的 role-local session 名称不能产生伪独立来源 | Knowledge Ledger |

---

## 3. 架构平面与依赖方向

```text
┌──────────────────────────────────────────────────────────────┐
│ 6. Audit & Observability                                     │
│    AuditWriter · SQLite canonical records · ReadStore · SSE │
└──────────────────────────────▲───────────────────────────────┘
                               │ redacted append / atomic finalize
┌──────────────────────────────┴───────────────────────────────┐
│ 5. Authority                                                 │
│    CompletionAuthority · FinalVerifier · Verdict             │
└──────────────────────────────▲───────────────────────────────┘
                               │ non-authoritative state
┌──────────────────────────────────────────────────────────────┐
│ 4. Evidence & Knowledge                                      │
│    ArtifactStore · Evidence Archiver · Knowledge Ledger      │
└──────────────────────────────▲───────────────────────────────┘
                               │ validated reports and evidence
┌──────────────────────────────────────────────────────────────┐
│ 2. Control                                                   │
│    Run Controller · bounded tree scheduler · turn protocol   │
└───────────────┬──────────────────────────────▲───────────────┘
                │ runtime requests             │ normalized events
┌───────────────▼──────────────────────────────┴───────────────┐
│ 3. Execution                                                 │
│    AgentRuntimeFactory · AgentSession · ProcessStarter       │
└──────────────────────────────────────────────────────────────┘
                               ▲
┌──────────────────────────────┴───────────────────────────────┐
│ 1. Contract                                                  │
│    RunDescriptor · TargetSpec · Policy · states · ports      │
└──────────────────────────────────────────────────────────────┘
```

### 3.1 平面职责

| 平面 | 唯一职责 | 不负责 |
|---|---|---|
| 1. Contract | 定义可序列化输入、不变量、端口和状态语义 | 启动进程、访问数据库、决定语义结论 |
| 2. Control | 拥有单个 Run 的可变状态，执行调度、预算、取消和终结转换 | 实现 Pi RPC、Docker、TSec、SQLite 或 UI |
| 3. Execution | 把 runtime-neutral 请求翻译成具体 Agent/进程行为 | 判断 Run 是否成功、生成 Knowledge |
| 4. Evidence & Knowledge | 把不可信提议转换为可追溯证据与受控知识 | 使用 Agent 文本宣告成功、直接写原始 Store |
| 5. Authority | 只依据外部验证或 backend completion 产生最终 verdict | 接管探索调度、修改证据 |
| 6. Audit & Observability | 脱敏、追加、原子终结、查询、重放 | 改变控制状态、推导新的成功结论 |

### 3.2 依赖规则

- 根 `harness` 包只依赖 Go 标准库。
- 外围模块只能依赖根包和 `internal/` 工具；根包不得依赖 Pi、Docker、TSec、SQLite、CLI、HTTP 或 Web。
- `pi`、`sandbox/docker`、`tsec`、`verify`、`store/sqlite` 是实现端口的适配器，不得导入控制循环。
- `internal/cli` 是 composition root；HTTP 和 Web 不能自行拼装第二套探索逻辑。
- 所有跨平面写入经过接口；禁止适配器直接修改 controller 内存状态。

---

## 4. 领域模型与状态机

### 4.1 稳定身份与运行绑定

```go
type TargetSpec struct {
    ID        string         `json:"id" yaml:"id"`
    Addresses []string       `json:"addresses,omitempty" yaml:"addresses,omitempty"`
    Isolation IsolationMode  `json:"isolation" yaml:"isolation"`
}

type TargetBinding struct {
    TargetID  string
    Addresses []string
    Runtime   RuntimeBinding
}

func TargetFingerprint(spec TargetSpec) string
```

规则：

- `TargetSpec.ID` 是调用方声明的稳定逻辑身份，是知识跨 Run 复用的唯一 Target 身份来源。
- `TargetSpec.Addresses` 是本 Run 允许引用的初始地址；动态 backend 可以在 `TargetBinding` 中给出本次实例地址。
- `TargetBinding.TargetID` 必须等于 `TargetSpec.ID`；binding 不得改变授权 identity 或 isolation。
- 容器名、Compose project、IP、端口、挑战实例和容器状态属于 `TargetBinding.Runtime`，永不进入 `TargetFingerprint`。
- 调用方复用同一个 `TargetSpec.ID` 即声明这些 Run 指向同一逻辑目标；把不同环境复用为同一 ID 是调用方错误。
- Observation 和 Branch 中出现的 target 引用必须在写入前对照当前 `TargetSpec` 与 `TargetBinding` 校验。

`TargetFingerprint` 的规范实现是规范化后的 `TargetSpec.ID`。实现中必须只保留一个指纹函数，删除公共与内部双实现。

### 4.2 Run 生命周期

Run 生命周期拆成两个正交字段：

```go
type RunPhase string

const (
    RunPhaseQueued       RunPhase = "queued"
    RunPhaseProvisioning RunPhase = "provisioning"
    RunPhaseRunning      RunPhase = "running"
    RunPhaseFinalizing   RunPhase = "finalizing"
    RunPhaseVerifying    RunPhase = "verifying"
    RunPhaseTerminal     RunPhase = "terminal"
)

type RunOutcome string

const (
    RunOutcomeSucceeded   RunOutcome = "succeeded"
    RunOutcomeFailed      RunOutcome = "failed"
    RunOutcomeCancelled   RunOutcome = "cancelled"
    RunOutcomeTimedOut    RunOutcome = "timed_out"
    RunOutcomeInterrupted RunOutcome = "interrupted"
    RunOutcomeStagnated   RunOutcome = "stagnated"
)
```

状态机：

```text
queued
  └─> provisioning
        └─> running
              ├─> finalizing ─> verifying? ─> terminal
              ├─> verifying ─────────────────> terminal   // 无独立收尾资源时
              └─> finalizing ────────────────> terminal   // backend 已完成/取消/内部失败
```

转换规则：

- `queued`、`provisioning`、`running`、`finalizing`、`verifying` 时 `Outcome` 为空。
- `RunPhaseTerminal` 时 `Outcome` 必须非空；到达 terminal 后任何转换都被拒绝。
- backend completion 是权威事件，可在 `running` 中直接触发取消和 finalization。
- 取消、超时、内部错误先进入 finalization，确保 session 和 target 已请求清理；清理失败会覆盖成功结果。
- 对外 API 可提供一个只读的兼容 `Status` 投影，但内部和持久化的 canonical 字段必须是 `Phase + Outcome`。
- `interrupted` 只用于启动恢复，不是正常执行路径。

### 4.3 Branch 与 Attempt

Branch 表示一个可验证假设，Attempt 表示该假设的一次执行。状态拆成“生命周期”和“结论”：

```go
type BranchPhase string
// open -> active -> open | closed
// root 只处于 open/closed，不参与调度

type BranchOutcome string
// pending | supported | refuted | blocked | inconclusive | abandoned | resolved

type AttemptPhase string
// running | completed | truncated | failed | cancelled

type AttemptOutcome string
// supported | refuted | blocked | inconclusive
```

规则：

- `AttemptPhase` 回答“执行如何结束”；`AttemptOutcome` 回答“语义结论是什么”。
- `AttemptPhase` 为 `failed`、`cancelled` 或 `truncated` 时，`AttemptOutcome` 可以为空。
- `local_success` 更名为 `supported`，避免暗示 Run 成功。
- 只有 `AttemptPhase == completed && AttemptOutcome == supported` 且至少有一个已归档 Observation 时，才允许创建 hypothesis 子分支。
- Planner 只能创建 root 直接子节点；非 root 扩展必须来自成功 Attempt 的 `ChildBranches`。
- root 不受 `MaxChildrenPerBranch` 限制，但仍受 `MaxNodes` 与 `MaxDepth` 限制。
- Review branch 必须精确绑定两个 Knowledge ID；只有同时支持一方、反驳另一方的一次报告可以解除 contested。
- 状态转换只由 Run Controller 提交，Attempt goroutine 只能返回结果。

### 4.4 探索调度

- 继续使用确定性 round barrier：每轮按 priority、depth、ID 排序选择最多 `MaxActiveBranches` 个 ready branches，并发执行，全部结束后按确定性顺序归并。
- 实际并发还受 Executor 数量和 `Runner.MaxSessions` 限制。
- 每轮最多有一个 Planner turn；每个被调度 Branch 最多有一个 Attempt。
- 任何一个并发执行返回不可恢复的 canonical 错误时，controller 停止启动新工作，取消在飞工作并进入 finalization。
- `MaxDepth`、`MaxNodes`、`MaxChildrenPerBranch`、`MaxAttemptsPerBranch`、`MaxToolCallsPerAttempt`、`MaxPlannerTools`、`MaxPlannerWallTime`、上下文和证据上界均由 Go 强制。
- 下列任一条件结束探索：外部权威完成、预算耗尽、无 ready branch 且 planner 无新增、达到停滞轮数、控制错误、取消。
- 分支内容由模型提出；分支身份、拓扑约束和接受与否由代码决定。

### 4.5 预算与上下文

预算按所有者分组，而不是塞进一个含义混合的 policy：

| 预算 | 所有者 | 例子 |
|---|---|---|
| RunBudget | Run Controller | wall time、tokens、model calls、tool calls、cost |
| ExplorationPolicy | tree scheduler | depth、nodes、children、active branches、attempts、stagnation |
| TurnLimits | turn protocol | 单 turn tool calls、tool timeout |
| ContextPolicy | context builder | knowledge digest 条数/字节、最近报告窗口 |
| EvidencePolicy | Evidence Archiver | 文件数、单文件字节、单 Attempt 总量 |
| KnowledgePolicy | Knowledge Ledger | 条目上限、statement 字节、review 数量 |

零值处理必须显式：默认值由 `Normalize*` 在进入 Run 前一次性填充；Run 内不再使用“零代表默认”的隐式分支。非法负值在启动前拒绝。

模型上下文必须：

- 由 controller 在 session Reset 前构造；
- 具有确定性排序和硬截断；
- 标注被截断项数量并写入事件；
- 排除 refuted、过期和不可复用知识；
- 不包含已知 secret；
- 不包含其他 Task 或未授权 Target 的数据。

---

## 5. 目标公共契约

### 5.1 Run 输入

```go
type RunDescriptor struct {
    SchemaVersion int
    Task          Task
    Target        TargetSpec
    Planner       AgentSpec
    Executors     []AgentSpec
    Policy        RunPolicy
}

type RunSpec struct {
    Descriptor RunDescriptor
    Provisioner TargetProvisioner
    Completion  CompletionAuthority
    Verifier    FinalVerifier
    Artifacts   ArtifactStore
}

type RunPolicy struct {
    Budget      Budget
    Exploration ExplorationPolicy
    Context     ContextPolicy
    Evidence    EvidencePolicy
    Knowledge   KnowledgePolicy
}
```

边界：

- `RunDescriptor` 是可序列化的 CLI/HTTP/审计输入，不包含函数、进程或数据库句柄。
- `RunSpec` 在 composition root 绑定适配器实例。
- `RunSpec` 校验失败时不得创建 Run 记录。
- HTTP descriptor 中的镜像、挂载、命令、能力和资源字段只在“受信操作者”前提下接受；若未来支持不可信客户端，必须新增服务端 allowlist 和认证层，不能只依赖 JSON 校验。

### 5.2 权威端口

```go
type CompletionAuthority interface {
    Observe(context.Context, TargetBinding) (Completion, error)
}

type FinalVerifier interface {
    Verify(context.Context, VerifyRequest) (Verdict, error)
}
```

语义：

- `CompletionAuthority.Observe` 可在 Run 期间重复调用；只有 `Done=true` 的结果可以冻结权威结论。
- 第一次 `Done=true` 的结果获胜。之后的 completion 被记录为 ignored，不能覆盖终态。
- `Done=false` 只更新非权威 progress；不得写入 Knowledge，也不得终止 Run。
- `FinalVerifier` 只在探索结束且没有已完成 backend 时调用一次。
- Verifier 进程错误、超时、无效 JSON 或空 verdict 都使 Run `failed`。
- 既没有 completion authority 也没有 final verifier 的 Run 必须 `failed`，错误为 `no authoritative verdict source`。
- Provisioner 可以同时实现 CompletionAuthority，但类型上仍是两个能力；启动 Target 本身不产生任何成功含义。

### 5.3 持久化端口

```go
type AuditWriter interface {
    CreateRun(context.Context, RunRecord) error
    UpdateRun(context.Context, RunRecord) error
    AppendEvent(context.Context, Event) (Event, error)
    AppendLog(context.Context, LogRecord) (LogRecord, error)
    SaveBranch(context.Context, Branch) error
    SaveAttempt(context.Context, Attempt) error
    SaveObservation(context.Context, Observation) error
    SaveArtifact(context.Context, Artifact) error
    SaveKnowledgeVersion(context.Context, Knowledge) error
    Finalize(context.Context, FinalizationRecord) (Event, error)
}

type ReadStore interface {
    GetRun(context.Context, string) (RunRecord, error)
    ListRuns(context.Context, int) ([]RunRecord, error)
    ListEvents(context.Context, string, int64) ([]Event, error)
    ListLogs(context.Context, string, int64) ([]LogRecord, error)
    GetVerdict(context.Context, string) (VerdictRecord, error)
    ListBranches(context.Context, string) ([]Branch, error)
    ListAttempts(context.Context, string) ([]Attempt, error)
    ListObservations(context.Context, string) ([]Observation, error)
    ListArtifacts(context.Context, string) ([]Artifact, error)
    QueryKnowledge(context.Context, KnowledgeQuery) ([]Knowledge, error)
}
```

规则：

- `AuditWriter` 在构造时绑定 Run scoped Redactor；实现必须在内部完成脱敏，调用方不得要求“先自己脱敏再写入”。
- Agent 可见配置文件的脱敏是另一个边界，由 Runtime adapter 使用同一个 Redactor 完成。
- `Finalize` 必须原子提交 Run 终态、Verdict 和 `run.finalized` 事件。
- 写失败后可以 best-effort 标记 failed，但不得继续推进原 Run。
- `ReadStore` 不返回可变控制对象；读取失败不能改变 Run。
- ArtifactStore 继续独立：内容寻址、按字节流写入、metadata 由 canonical store 记录。

### 5.4 Event 契约

```go
type Event struct {
    Seq           int64
    SchemaVersion int
    RunID         string
    SessionID     string
    Type          EventType
    Time          time.Time
    Payload       json.RawMessage
    Raw           json.RawMessage
}
```

规则：

- `EventType` 使用枚举常量，payload 使用明确的 Go struct；禁止 `map[string]any` 作为 canonical payload。
- `Raw` 只保存 adapter 收到的原生事件，供审计；控制逻辑只读取规范化字段。
- 同一事件写入时必须确定 `SchemaVersion`，不得由读取端猜测。
- `Seq` 由 store 单调分配；SSE 以 `Last-Event-ID`/`after_seq` 重放。
- 读取端和 Web 对未知 `Type` 或未知 `SchemaVersion` 必须降级为“不可视事件”，不能让整个 Run 页面失败。
- 事件 payload 中不出现已知 secret；原始 agent 事件同样经过 Redactor。

最小事件集合：

| 事件 | 所有者 | 必要语义 |
|---|---|---|
| `run.created` | Runner | Run 已持久化，探索尚未开始 |
| `run.phase` | Controller | phase 转换 |
| `run.provisioned` | Controller | 已得到 TargetBinding |
| `agent.session.started` | Controller | runtime session 已创建 |
| `agent.event` | Turn Protocol | 规范化后的 Agent 事件与原始审计记录 |
| `agent.log` | Execution adapter | stdout/stderr 日志 |
| `exploration.branch.created` | Controller | Branch 已接受 |
| `exploration.branch.updated` | Controller | phase/outcome/priority 变化 |
| `exploration.branch.rejected` | Controller | 模型提议被机械规则拒绝 |
| `exploration.attempt.started` | Controller | Attempt 开始 |
| `exploration.attempt.completed` | Controller | 执行 phase 与语义 outcome |
| `exploration.attempt.failed` | Controller | fail-closed 的 Attempt 错误 |
| `artifact.stored` | Evidence Archiver | 内容寻址证据已落盘 |
| `observation.created` | Evidence Archiver | 当前 Attempt 的证据解释 |
| `knowledge.promoted` | Knowledge Ledger | 新知识版本 |
| `knowledge.supported` | Knowledge Ledger | 独立来源复证 |
| `knowledge.contested` | Knowledge Ledger | 冲突进入 review |
| `knowledge.refuted` | Knowledge Ledger | 明确裁决后的否定 |
| `authority.progress` | Authority service | 非权威进度 |
| `authority.completed` | Authority service | 权威完成结果 |
| `run.finalized` | Controller/AuditWriter | 终态、结果和 verdict 已原子提交 |

### 5.5 Run Handle

公开的 `Run` 只是控制与观测句柄：

```go
type RunHandle interface {
    ID() string
    Done() <-chan struct{}
    Events() <-chan Event
    Wait(context.Context) (Result, error)
    Cancel(context.Context) error
    Steer(context.Context, SessionID, string) error
}
```

- Handle 不拥有 scheduler、branch、attempt 或 authority 状态。
- `Cancel` 只发出 run-scoped cancellation；状态转换由 controller 完成。
- `Steer` 只在目标 session 存在时接受，且消息仍进入审计。
- `Wait` 返回已经持久化的终态快照；不能返回未 finalize 的成功。

---

## 6. 模块本质目录

### 6.1 核心控制与契约

| 模块 | 本质 | 拥有的状态 | 禁止承担 |
|---|---|---|---|
| `Runner` | 进程级生命周期与 composition boundary | active Run handles、恢复扫描、全局会话上限 | 不拥有单个 Run 的探索状态；不执行 LLM 语义判断 |
| Run Controller | 单 Run 唯一状态写者和调度器 | phase/outcome、tree、attempts、budgets、authority snapshot | 不解析 Pi wire format；不直接实现 Store |
| Run Handle | 对调用方暴露控制与结果的 facade | 只读终态快照引用、done/event channels | 不允许回调修改 controller 状态 |
| Turn Protocol | 一次 prompt 到 settled 的有限状态消费器 | 单 turn tool 计数、当前 tool timeout | 不解释 Agent 文本语义；不调用 Verifier |
| Planner Strategy | 将有界上下文转化为 root-level 提议 | 无持久状态 | 不能扩展非 root 节点；不能写 Store |
| Executor Strategy | 将 branch 转化为 Attempt report | 无持久状态 | 不能创建 ID、Archive 文件或裁决成功 |
| Context Builder | 构造确定性、有界的模型视图 | 无持久状态 | 不读取全量无界历史；不注入 refuted 知识 |
| Redactor | 已知 secret 的单向清洗器 | secret value set（按 Run 作用域） | 不判断语义、不访问网络 |
| AuditWriter | canonical、append-only、强制脱敏的写入边界 | 写入事务与 sequence | 不决定状态转换；不向调用方泄漏 raw store |
| ReadStore | 投影查询边界 | 无控制状态 | 不推导新的 trust、success 或 evidence |
| SQLite adapter | 持久化实现 | DB、artifact CAS、schema version | 不包含探索策略或 runtime 语义 |

所有控制状态转换必须经过一个函数入口，例如 `RunController.apply(transition)`。其他模块只能提交命令或返回结果，不能直接修改 controller 内部字段。

### 6.2 执行与权威

| 模块 | 本质 | 输入 → 输出 | 禁止承担 |
|---|---|---|---|
| `AgentRuntimeFactory` | 创建可 reset 的不可信 Agent session | `RuntimeRequest` → `AgentSession` | 不判断 Run 成功；不拥有 branch |
| `AgentSession` | 单向 prompt/event/log/usage 通道 | prompt/steer/reset → normalized events | 不直接写 canonical store；不以 channel close 表示成功 |
| Pi adapter | Pi 进程与 RPC 的协议翻译 | RuntimeRequest → Pi process + events | 不实现探索控制；不解析报告成功 |
| Docker adapter | 容器和 Target 资源的硬化启动/清理 | `ProcessSpec` / backend spec → process/target binding | 不提供 verdict；不访问 Docker socket 之外的控制面 |
| TSec adapter | 授权挑战 provisioning 与 backend completion | challenge config → binding/completion | 不提交 flag；不根据 Agent 文本宣告成功 |
| Command Verifier | 外部权威验证器 | VerifyRequest → Verdict JSON | 不获得控制循环写权限；不修改证据 |
| Finalizer | 冻结 terminal state 并原子提交 | terminal transition → stored result/event | 不重试已完成副作用；不把清理错误吞掉 |

### 6.3 证据与知识

| 模块 | 本质 | 拥有的状态 | 禁止承担 |
|---|---|---|---|
| Evidence Archiver | 把不可信文件提议转换为真实 Artifact/Observation | 单 Attempt 的文件数、字节和路径校验 | 不信任报告中的 hash/ID；不生成语义成功 |
| ArtifactStore | 内容寻址字节存储 | artifact bytes 与 hash | 不保存结果或 verdict；不解释内容含义 |
| Knowledge Ledger | 执行 trust、冲突、review 和复用规则 | knowledge versions、sources、contradicts | 不让模型直接设置 trust；不产生 Run verdict |
| Review Resolver | 只处理绑定的 contested pair | 两个知识 ID 的裁决结果 | 不根据相似文本推断冲突；不支持静默覆盖 |
| Secret Scanner/Redactor | 阻止已知秘密进入 canonical records 和 agent-visible 配置 | known secret values | 不把不确定匹配当作用户语义 |

### 6.4 外围 interface

| 模块 | 本质 | 允许读取/写入 | 禁止承担 |
|---|---|---|---|
| CLI | composition root 与操作入口 | 配置、SDK、adapters | 不实现探索或 trust 规则 |
| HTTP/SSE | 受信操作者的控制与只读观测 API | RunHandle、ReadStore | 不直接操作 controller 内部状态；不自行判定成功 |
| Web console | canonical records 的只读投影 | HTTP/SSE、typed events | 不从文案或工具日志推导成功；不阻塞未知事件 |
| WebUI embed | 将构建产物嵌入 CLI server | static FS | 不包含业务逻辑 |
| Skills | 领域流程建议与工具选择 | Agent prompt/task files | 不能建立 Run verdict 或绕过 evidence 规则 |
| Pi/Kali image | 预装工具与运行环境 | container | 不包含授权决定或持久化权威 |

---

## 7. 主数据流

```text
Caller / CLI / trusted HTTP operator
  │
  ├─ RunDescriptor validation
  ├─ RunSpec adapter binding
  ▼
Runner
  ├─ create canonical Run(queued)
  ├─ recover unfinished historical runs as interrupted
  └─ start Run Controller
          │
          ├─ TargetProvisioner.Provision(TargetSpec)
          │      └─ TargetBinding(TargetID, addresses, runtime)
          │
          ├─ start Planner + Executors via AgentRuntimeFactory
          │
          ├─ round loop
          │    ├─ Context Builder → PlannerStrategy → root proposals
          │    ├─ deterministic ready-branch selection
          │    ├─ concurrent Attempts via AgentSession
          │    │    └─ Turn Protocol → normalized events / tool budget
          │    ├─ parse AttemptReport
          │    ├─ Evidence Archiver → Artifact → Observation
          │    ├─ Knowledge Ledger → validated versions/events
          │    └─ deterministic state merge
          │
          ├─ CompletionAuthority observed? ── yes → freeze authority
          ├─ no authority yet and exploration ended?
          │      └─ FinalVerifier.Verify once
          │
          └─ Finalizer
               ├─ cancel/close sessions
               ├─ close target binding
               ├─ map errors to terminal outcome
               └─ AuditWriter.Finalize(result, verdict, run.finalized)
                         │
                         ▼
                  ReadStore / SSE / Web
```

关键顺序：

1. TargetSpec 在任何 agent session 启动前完成规范化并写入 Run descriptor。
2. TargetBinding 在任何 branch/observation 被持久化前确定。
3. Evidence 写入先于 Observation，Observation 写入先于 Knowledge。
4. Authority completion 到达时立即阻止新 branch 调度，并取消在飞 turn。
5. Finalization 是唯一可以写 terminal Run、Verdict 和 `run.finalized` 的事务。
6. `run.finalized` 写入后，event stream 不再接受任何事件。

---

## 8. 当前实现审计与目标裁决

以下偏差来自 2026-10-24 的当前代码阅读与测试基线，不代表实现已经修改。

| 当前事实 | 风险 | 目标裁决 |
|---|---|---|
| `Runner`、`Run` 与 `explorationEngine` 共同保存和修改运行状态 | 状态所有权分散，取消、completion 和归并可出现双写或难以验证的时序 | 引入单 Run Controller；其他对象只提交命令/返回结果 |
| `Run.Status` 同时表示 phase 和 terminal outcome | 终态判断依赖长 switch，UI/持久化语义容易漂移 | 拆为 `RunPhase + RunOutcome`，兼容投影只读 |
| `AttemptStatus` 既含 completed/failed，又与 `AttemptOutcome`/`local_success` 交叠 | “本地成功”容易被误读为 Run 成功，失败语义重复 | 拆为 `AttemptPhase + AttemptOutcome`，`local_success` 改为 `supported` |
| 公开 `TargetFingerprint` 只使用 ID，内部 `targetFingerprint` 还使用地址 | 同一逻辑 Target 在不同运行实例上的知识复用可能不一致 | 只保留 `TargetFingerprint(TargetSpec)`；binding 细节不进入身份 |
| `Target.Runtime` 与逻辑 Target 共用同一结构 | 容器/挑战实例细节污染授权模型和序列化描述 | 分离 `TargetSpec` 与 `TargetBinding` |
| 复证只按 `SessionID` 去重，而 `executor-1` 这样的角色 ID 会在不同 Run 重复 | 跨 Run Knowledge 可能把两个 Run 的同名 executor 误判为独立来源 | 以 `(RunID, SessionID)` 作为来源身份 |
| Branch proposal 校验了 target，但 Observation proposal 的 target 原样持久化 | 审计中可能出现未授权 target 文本 | 在 Evidence Archiver 写入前统一校验 `Observation.Target` |
| Event 使用字符串 type 与无类型 JSON payload | 生产端和 Web 消费端容易分叉，未知事件可能拖垮页面 | 引入 typed/versioned event；未知事件安全忽略 |
| Web 中仍存在旧探索事件解析器，而当前引擎使用 branch/attempt 词汇 | 同一 UI 实际维护两套事件语义，旧路径可能成为死代码或误导 | 以一个事件契约生成/校验 TS 类型；迁移后删除旧解析路径 |
| Redactor 在多个调用点显式执行，同时 Store 包装层再次执行 | 容易出现漏路径、重复执行和“看似安全”的误判 | canonical 写入只由一个 AuditWriter 负责脱敏；agent-visible 配置单独处理 |
| Store 同时承担写入、查询和多个平面记录 | 适配器容易越权；写入路径难以统一脱敏和事务 | 拆为 `AuditWriter` 与 `ReadStore`，SQLite 同时实现两者 |
| HTTP 控制器接受完整 Agent/Target descriptor，默认无认证 | 远程不可信输入可能决定镜像、挂载、能力和环境 | 明确 trusted-operator 边界；默认 loopback；外部暴露必须增加认证与 allowlist |
| `Target.Isolation` 只有语义校验，网络 ACL 由调用方负责 | 用户可能误以为 SDK 已实现网络隔离 | 文档、事件和 API 明示 `caller_managed`，不新增虚假保证 |

这些偏差必须在重构实施计划中逐项映射到测试；本文不把它们当作已修复事实。

---

## 9. 失败关闭语义

| 场景 | 处理 | 是否可继续原 Run |
|---|---|---|
| descriptor/schema/必填字段非法 | 启动前拒绝，不创建 Run | 否，调用方可修正后新 Run |
| target isolation 不受支持 | 启动前或 provisioning 阶段失败 | 否 |
| target provisioning 失败 | `failed`，关闭已创建资源 | 否 |
| session 启动失败 | `failed`，关闭已启动 session/target | 否 |
| Planner/Executor 报告 malformed | 当前 Attempt 失败；按重试规则处理 | 是，若 Attempt 还未耗尽 |
| Branch/child 超出机械限制 | 拒绝提议并写 rejected 事件 | 是 |
| Observation target 未授权 | 整个 Attempt 失败，不写 Observation | 是，若重试预算仍允许 |
| 证据路径绝对/穿越/symlink escape/非普通文件 | 整个 Attempt 失败，不写 Artifact metadata | 是，若重试预算仍允许 |
| 单个证据超限/总量超限 | 整个 Attempt 失败 | 是，若重试预算仍允许 |
| 可读文本包含已知 secret | 脱敏后归档，并写 audited redaction marker | 是 |
| 二进制证据包含已知 secret | 拒绝归档，Attempt 失败 | 是，若重试预算仍允许 |
| ArtifactStore 写失败 | canonical failure，取消探索 | 否 |
| AuditWriter 任一写入失败 | canonical failure，进入失败终结路径 | 否 |
| Agent event channel 在 settled 前关闭 | turn 失败，Attempt 失败 | 是，按 Attempt 重试规则 |
| Agent malformed framing/RPC | turn 失败并 abort session | 是，按 Attempt 重试规则 |
| tool 超时或 tool budget 超限 | abort turn，Attempt 失败 | 是，按 Attempt 重试规则 |
| Run cancellation | cancel batch/turn，进入 finalization | 否，终态 cancelled |
| backend `Done=true, Success=false` | 立即权威失败 | 否 |
| 重复 backend completion | 第一次为权威；后续记录 ignored | 否 |
| FinalVerifier 进程错误/超时/无效 JSON | `failed` | 否 |
| 没有可用权威 | `failed` | 否 |
| target close 失败且当前结果成功 | 将结果降为 `failed` | 否 |
| event stream 已 finalize 后仍有写入 | 拒绝写入并报告 control error | 否 |

所有“可继续”的失败只能影响当前 Attempt 或 Branch，不能绕过 canonical store；所有“不可继续”的失败必须最终由一个 terminal outcome 表示。

---

## 10. Knowledge 语义

### 10.1 身份与来源

- Knowledge 来源链必须是：`KnowledgeVersion → KnowledgeSource → Observation(current Attempt) → Artifact(content hash)`。
- `ObservationRefs` 只引用当前 Attempt 的 Observation；模型不能提供 Artifact ID、hash 或跨 Attempt 的 Observation ID。
- Knowledge identity key 至少由以下字段规范得到：`TaskID + normalized Topic + Kind + Scope + TargetFingerprint`。
- Statement 不是身份的一部分；同一 key 的新陈述必须以 `Supports` 或 `Refutes` 显式表达，不能被静默替换。
- Knowledge 的 ID、版本号、来源和 trust 均由代码生成；模型只能提出内容与关系。
- `BENCHMARK_TOKEN`、flag 和其他已知 secret 永远不得进入 Knowledge。

### 10.2 Trust 转换

```text
new claim ─────────────────────────────> observed
observed/corroborated + 独立 session 支持 -> corroborated
observed/corroborated + refute ─────────> contested + review branch
contested + 有效 review 裁决 ───────────> 胜方 corroborated / 负方 refuted
refuted ───────────────────────────────> refuted（不可自动恢复）
```

规则：

- 复证必须来自不同 `(RunID, SessionID)`；同一 Run 内同名 executor 或跨 Run 的 `executor-1` 都不能自动构成独立来源。
- 任何时刻 `corroborated` 必须至少有两个独立 session source。
- `refuted` 只能由绑定两个 Knowledge ID 的 review 产生。
- 冲突不允许静默覆盖：被反驳条目不直接变 refuted，而是双方进入 contested，等待 review。
- `verified` Knowledge 不存在；Run 的成功来源是 Verdict，不是 Knowledge trust。
- Review 报告必须恰好支持绑定 pair 的一方并反驳另一方；否则拒绝，原 pair 保持 contested。

### 10.3 跨 Run 复用矩阵

目标态将当前重叠的 scope/reuse 语义收敛为一个明确的适用范围字段，并保留 `ExpiresAt`。若实现阶段需要兼容旧字段，旧字段只能映射到本矩阵，不能保留第二套判断逻辑。

| KnowledgeScope | 同 Task | 同稳定 Target | 跨 Run | 注入条件 |
|---|---:|---:|---:|---|
| `run` | 必须 | 不要求 | 否 | 当前 Run 内、未过期、非 refuted |
| `target` | 必须 | 必须 | 是 | 同 TargetFingerprint、未过期、非 refuted |
| `task` | 必须 | 不要求 | 是 | 同 TaskID、未过期、非 refuted |

统一过滤顺序：

1. `TaskID` 必须匹配；
2. `Trust != refuted`；
3. `ExpiresAt == nil || ExpiresAt > now`；
4. scope 对应的 RunID / TargetFingerprint 必须匹配；
5. 按 trust、`LastValidated`、ID 确定性排序；
6. 按 ContextPolicy 的条数与字节截断，并记录 dropped 数量。

`contested` 可以注入，但必须显式标注“存在冲突/不得单独依赖”；排序低于 corroborated 和 observed。过期或 refuted 的知识永不注入。

---

## 11. 安全与信任边界

### 11.1 Agent 与 Shell

Agent、模型输出、Shell 输出和工具退出码均不可信。Runtime 必须：

- 使用严格 framing；Pi 只用 LF JSONL；
- 把 `agent_settled` 作为唯一 turn completion；`agent_end` 不是完成；
- 在 settlement 前关闭 event channel 时失败关闭；
- 限制工具调用和单工具 wall time；
- 不让 Agent 获得 Docker socket；
- 保持 read-only root、`no-new-privileges`、`cap-drop ALL`，只显式添加所需 capability；
- 限制 CPU、内存、PID，并只挂载显式 Task/Skills/Workspace；
- 不把 secret 放进 argv；使用环境变量或受控文件，并经过 Redactor。

### 11.2 网络与 Target 授权

- v1/目标态只支持 `caller_managed`。
- SDK 执行的是语义 Target 引用校验，不是网络 ACL。
- 调用方负责容器 egress、VPN、DNS 和控制端可达性。
- Docker runtime 只确保容器不会获得 Docker 控制面，不承诺目标网络隔离。
- Target ID 是知识复用的稳定命名；地址是本次 binding 的实际入口。
- 任何 target 字段在进入 branch、observation、prompt 或知识之前都必须经过同一个授权函数。

### 11.3 秘密

- 已知 secret 来自 `SecretEnv`、Agent inline env 和相关 adapter 配置；Runner 在每个 Run 开始前构建 Run scoped Redactor。
- 所有 canonical 写入必须由 AuditWriter 自动脱敏。
- 文本 Artifact 哈希前脱敏；二进制 Artifact 命中已知 secret 时拒绝归档。
- Agent-visible 配置文件由 Runtime adapter 在写入前脱敏。
- 日志、错误、SSE、Result、Verdict 和 Knowledge 不得包含 `BENCHMARK_TOKEN` 明文。
- Redaction marker 可以记录“已发生脱敏”，但不能记录原值或可用于恢复的片段。

### 11.4 HTTP 控制面

- HTTP 控制面面向受信操作者，不面向不可信多租户客户端。
- CLI 默认监听 loopback；外部监听必须由操作者显式配置并自行提供认证/网络保护。
- descriptor 可以携带镜像、挂载、命令、能力和资源字段，但这些属于受信部署配置，不属于远程安全边界。
- 若未来需要不可信客户端，必须增加认证、授权、服务端 allowlist、资源配额和审计主体，不能仅依赖当前 JSON schema。

---

## 12. 公共 API 演进与迁移阶段

### 12.1 兼容原则

- 允许为消除身份、状态和审计语义冲突做必要破坏。
- 不为语义冲突保留两套长期并存的实现；兼容 shim 只能单向映射到一个 canonical 语义。
- 不恢复 Workflow/direct-workflow、两套 frontier/exploration 引擎或隐式 resume。
- SQLite schema 演进只允许增列、建表、索引和可回填转换，不删除既有 events/logs。
- Event schema 版本升级后，旧 reader 可以拒绝新事件，但新 reader 必须能安全读取旧事件和未知事件。

### 12.2 阶段

#### Phase 0：契约冻结

- 冻结本设计中的 `TargetSpec`/`TargetBinding`、`RunPhase`/`RunOutcome`、`AttemptPhase`/`AttemptOutcome`、authority 和 AuditWriter 语义。
- 先写编译期端口测试、状态可达性测试和当前行为基线的 golden/contract 测试。
- 确认 README、架构文档、CLI/HTTP/Web 对 v1 的描述与当前实现一致。

#### Phase 1：身份与状态

- 引入 `TargetSpec`/`TargetBinding`，统一 Target fingerprint。
- 引入 Run phase/outcome 和 Attempt phase/outcome。
- 增加兼容只读投影，迁移 UI 与 API；不保留第二套状态机。
- 所有 target authorization 收敛到一个函数。

#### Phase 2：控制所有权

- 引入单 Run Controller；Runner 只负责生命周期和 composition。
- 所有状态变更转换为单写者提交。
- 取消、completion、budget、cleanup 和 finalization 用同一个转换入口。
- 删除 controller 外对 Branch/Attempt/Knowledge 状态的可写路径。

#### Phase 3：审计与事件

- 拆分 `AuditWriter` 和 `ReadStore`。
- 在 canonical writer 内统一 Redactor。
- 引入 typed/versioned event 和 TS 类型同步/契约测试。
- SQLite 执行 additive migration；保留历史 events/logs，回填 phase/outcome 或通过只读兼容投影读取旧 status。
- 更新 SSE 与 Web，未知事件安全跳过。

#### Phase 4：适配器与外围

- Pi、Docker、TSec、Command Verifier 按新端口迁移，删除重复的 target/check/redaction 逻辑。
- CLI 保持 composition root；HTTP 固定 trusted-operator 文档和默认 loopback。
- Web 只消费 typed event 和 ReadStore 投影，删除旧探索事件解析路径。
- 更新 README、`docs/architecture.md`、`docs/pi.md`、`docs/tsec.md` 和迁移文档。

#### Phase 5：清理与验收

- 删除旧状态枚举、双重 fingerprint、旧事件分支和废弃 shim（shim 的保留周期必须在实施计划中明确）。
- 运行完整 Go 和 Web 验证矩阵。
- 对有环境依赖的 Docker integration、live TSec/VPN 检查单独标注，不虚报为已通过。

---

## 13. 追踪矩阵与测试计划

### 13.1 约束追踪矩阵

| 约束 | 强制模块 | 关键状态转换/校验 | 目标测试 |
|---|---|---|---|
| I1 权威成功 | Authority service + Finalizer | completion done → terminal；verifier success → terminal | `TestOnlyAuthorityCanSucceed` |
| I2 Agent 不能宣告成功 | Executor/Turn/Audit | report outcome 不进入 RunOutcome | `TestAgentReportCannotEstablishSuccess` |
| I3 单写控制者 | Run Controller | 所有 transition 经单一入口 | `TestControllerIsSingleWriterUnderConcurrentCompletion` |
| I4 Target 稳定身份 | TargetSpec/Fingerprint | runtime 变化不改 fingerprint | `TestTargetIdentityIgnoresBindingRuntime` |
| I5 binding 不越权 | Provisioner/Controller | binding.TargetID == spec.ID | `TestBindingCannotChangeAuthorizationIdentity` |
| I6 证据先于知识 | Evidence Archiver/Ledger | artifact → observation → knowledge | `TestKnowledgeRequiresCurrentAttemptObservation` |
| I7 代码生成 ID | Core | report 提供 ID 被忽略/拒绝 | `TestReportCannotForgeDomainIDs` |
| I8 单一脱敏边界 | AuditWriter/Redactor | 所有 canonical writes 自动扫描 | `TestAuditWriterRedactsEveryRecord` |
| I9 canonical 失败关闭 | AuditWriter/Controller | 写失败 → failed finalization | `TestCanonicalWriteFailureTerminatesRun` |
| I10 无隐式恢复 | Runner recovery | unfinished → interrupted | `TestRecoveryOnlyMarksInterrupted` |
| I11 有界上下文 | Context Builder | digest 条数/字节/排序 | `TestKnowledgeDigestIsBoundedAndDeterministic` |
| I12 状态可达 | State tests | 枚举转换全部可达 | `TestAllDeclaredStatesAreReachable` |
| I13 事件兼容 | Event/ReadStore/Web | unknown version/type 跳过 | `TestUnknownEventDoesNotBreakRunView` |
| I14 HTTP 信任边界 | CLI/Server docs | 默认 loopback；外部监听显式 | `TestServerDefaultsToLoopback` + docs check |
| I15 容器硬化 | Docker adapter | 无 socket；read-only；caps/resources | `TestDockerArgsAreHardened` |
| I16 独立复证来源 | Knowledge Ledger | `(RunID, SessionID)` 唯一性 | `TestCorroborationRequiresRunScopedSession` |

### 13.2 必测负例

1. malformed Planner/Executor JSON、空输出、错误 branch ID、未知 outcome。
2. Planner 尝试扩展非 root、重复 topic、越界 target、超过 child/node/depth 限制。
3. 证据绝对路径、`..`、symlink escape、目录、设备文件、大小超限、hash/size 不一致。
4. 文本 secret 脱敏、二进制 secret 拒绝、secret 不进入任何 SQLite 表或 Artifact 内容。
5. event channel 在 `agent_settled` 前关闭、malformed JSONL、RPC 错误、tool timeout、tool budget 超限。
6. backend 在探索中返回 `Done&&Success`、返回 `Done&&!Success`、重复返回不同结果、返回 `Done=false` 后继续探索。
7. verifier 进程失败、超时、无效 JSON、空 verdict、成功但 target close 失败。
8. audit/artifact 写失败，取消与 finalize 竞态，finalized event 后无写入。
9. knowledge 同 session 复证不得升级、跨 Attempt 引用不得接受、refuted/过期知识不得注入。
10. SQLite 迁移保留历史 events/logs、幂等重开、旧 status 兼容读取、未知新 schema 拒绝。
11. Web 收到旧事件、未知事件、损坏 payload 时局部降级，不丢失 Run 基本信息。
12. Docker 参数没有 Docker socket，read-only root、cap drop、no-new-privileges、PID/CPU/memory 限制仍在。

### 13.3 完成实现时必须运行的验证

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/harness
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
```

Docker integration 和 live TSec/VPN 测试只有在实际环境执行后才能报告通过。

### 13.4 本设计文档本身的验收

- 所有模块均有“本质、拥有、禁止承担、失败边界、扩展点”描述。
- 每个不可约不变量至少映射到一个强制模块和一个目标测试。
- 目标接口明确说明输入、输出、状态所有权和 fail-closed 行为。
- 当前实现偏差与目标裁决可逐项追溯到源码路径或现有测试。
- 文档中的分支、状态、知识转换和迁移规则不依赖模型文本或未运行的环境检查。
- 只新增本文档；不夹带旧收敛计划或其他未跟踪文件。

---

## 14. 非目标与明确假设

### 14.1 非目标

- 不增加 Workflow/direct-workflow 双控制路径。
- 不增加多 Mission、Campaign、多租户或分布式 worker。
- 不增加 checkpoint/resume、副作用重放或自动补偿。
- 不实现通用 LLM 路由、RAG、向量检索或自动知识总结。
- 不把安全领域本体（Web/AD/云/MITRE 等）加入根包。
- 不把 HTTP 控制面设计成可安全暴露给不可信客户端的产品；该能力需要独立设计。
- 不以事件溯源替换 SQLite canonical audit。

### 14.2 假设

- 每个 Run 恰好一个逻辑 Target；Target 可有多个地址。
- v1 只支持 `caller_managed` isolation。
- 有界假设树继续是唯一探索控制语义。
- Pi 是默认 Agent runtime；Docker 是默认 sandbox；TSec 是 backend；Command Verifier 是外部权威适配器。
- 同一 `Task.ID` 和稳定 Target 指纹内的知识可以跨 Run 复用，受 scope、expiry 和 trust 规则约束。
- HTTP 操作者受信，默认只监听本机。
- 允许为消除领域语义冲突进行必要公共 API 破坏；不保留双语义长期兼容层。
- 设计中的真实执行验证必须在实现阶段运行；本文只记录当前基线 `go test ./...` 已通过，不声称 race、Docker、TSec 或 Web 检查已通过。
