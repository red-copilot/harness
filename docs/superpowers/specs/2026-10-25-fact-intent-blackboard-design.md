# Fact / Intent 双执行槽设计

- 日期：2026-10-25
- 状态：Superseded by `2026-10-28-first-principles-harness-v2-design.md`; implementation history only
- 参考实现：Cairn (`oritera/Cairn`) 的 Fact/Intent/Hint blackboard 与 bootstrap/reason/explore 调度
- 影响面：探索模型、Planner/Executor 契约、Run Controller、SQLite、HTTP、Web、CLI 文档
- 迁移性质：新探索契约；旧 Branch/Knowledge 数据保留为只读历史

## 1. 意图

当前引擎让 Planner 一次生成多条 hypothesis，Executor 再从 attempt 派生 child branches。实际调度只消费少量分支，剩余分支长期 pending；同时每个 Attempt 必须先产出完整 `/workspace/report.json`、Observation、Knowledge 才算结束，导致探索与验证脱节。

真实基准证据：

| 题目 | 结果 | 耗时 | 累计 Branch | Attempts | 证据 |
|---|---|---:|---:|---:|---:|
| d-01 | 成功 | 50.8s | 5 | 1 | 0 |
| c-03 | 失败 | 270s | 5 | 1 | 0 |
| a-05 | 超时 | 605.8s | 10 | 3 | 0 |
| c-06 | 超时 | 605.8s | 14 | 3 | 0 |

Cairn 的已验证结构更接近问题本质：

```text
Fact  = 已确认的客观发现
Intent = 一个待执行的探索方向
Hint  = 人工或平台提示
Reason = 读取 Fact/Intent 图，决定下一步意图
Explore = 执行一个 Intent，产出一个 Fact
```

本设计采用 Cairn 的核心控制模型，但保留 Red Harness 的两条不可放弃约束：

1. Fact 必须能沿 Observation → Artifact 回溯；
2. Run 成功只能由 Verifier 或 backend completion 产生。

## 2. 不可约约束

- C1：Run 最多两个 Executor 槽位；每个槽位同时最多一个 active Attempt。
- C2：任一时刻最多两个 open/claimed Intent；不保留第三、第四条 pending intent。
- C3：Reason 是新 Intent 的唯一来源；Executor 不创建 Intent 或子节点。
- C4：一个 Intent 的目标是产出一个 Fact；执行失败时由 Conclude 收尾产出 negative/inconclusive Fact，或明确 abandoned。
- C5：positive Fact 必须引用当前 Attempt 已归档的 Observation/Artifact；模型描述本身不是证据。
- C6：Fact、Intent、Hint 只追加或做受控状态转换；Reason 不能改写已完成历史。
- C7：成功只能由 Verifier 或 backend completion 产生；Reason 的 goal candidate 不是完成。
- C8：所有 known secret 在 canonical 写入前脱敏；BENCHMARK_TOKEN 不进入 Fact、Intent、日志或错误。
- C9：任一 canonical 写入失败、malformed Reason/Explore 输出、backend 错误或清理失败都必须失败关闭。

## 3. 目标与非目标

### 3.1 目标

1. 用 Fact/Intent 图替代 Branch 树和 `ChildBranches`。
2. 两个固定 Executor 槽位直接执行 Intent；一个槽完成后立即补位，不等待另一槽。
3. Reason 在补位时读取最新图和最多两个空槽，生成 0–2 个新 Intent。
4. 加入 Bootstrap：初始状态先直接尝试解决整个任务，再由 Reason 展开。
5. 加入 Conclude fallback：Explore 超时或输出不可解析时，在同一 session 内要求停止探索并总结已确认结果。
6. Web 从 Branch tree 迁移为 Fact/Intent 图，并显示两个槽位与 Reason 活动。
7. 旧 Branch/Knowledge 运行仍可读取和展示。

### 3.2 非目标

- 不把 Cairn 的 Server + Dispatcher 拆分照搬进本仓库。
- 不引入通用 Worker 池、跨项目调度或多租户。
- 不移除 TSec backend、Pi、Docker、SQLite、Verifier。
- 不实现向量检索、RAG 或自动跨 Run 知识复用。
- 不在本阶段增加人工 Hint 编辑 UI；Hint 只读展示。
- 不提供 checkpoint/resume。

### 3.3 反目标

- Reason 一次返回超过空槽数量的 Intent。
- Executor 自己派生新 Intent。
- 第三个 open/claimed Intent 或隐藏 pending 队列。
- Explore 只写自然语言结论而没有 evidence 链。
- Reason 宣称 goal 完成即可结束 Run。
- 继续维护 Branch/Knowledge 两套可写控制语义。
- Conclude fallback 继续执行工具或扩大扫描范围。

## 4. 领域模型

### 4.1 Fact

```go
type FactKind string

const (
    FactOrigin    FactKind = "origin"
    FactGoal      FactKind = "goal"
    FactFinding   FactKind = "finding"
    FactNegative  FactKind = "negative"
)

type Fact struct {
    ID             string
    RunID          string
    TaskID         string
    Kind           FactKind
    Description    string
    SourceIntentID string
    ObservationIDs []string
    ArtifactIDs    []string
    CreatedAt      time.Time
}
```

- `origin` 和 `goal` 在 Run 初始化时创建，不来自模型。
- `finding` 必须引用至少一个当前 Attempt 归档的 Observation。
- `negative` 记录“已排除/无进展”的结论，可以没有 Artifact，但不能作为 goal evidence。
- Fact ID 由 controller 生成并带 Run 作用域；模型提供的 ID 不被信任。
- Fact 不可变；冲突通过新增 Fact 表达，不修改旧 Fact。
- Fact.Description 有字节上限，且不得包含 flag、token、凭据。

### 4.2 Intent

```go
type IntentState string

const (
    IntentOpen      IntentState = "open"
    IntentClaimed   IntentState = "claimed"
    IntentConcluded IntentState = "concluded"
    IntentAbandoned IntentState = "abandoned"
)

type IntentKind string

const (
    IntentBootstrap IntentKind = "bootstrap"
    IntentExplore   IntentKind = "explore"
)

type Intent struct {
    ID             string
    RunID          string
    Kind           IntentKind
    FromFactIDs    []string
    Description    string
    State          IntentState
    Slot           int
    ExecutorID     string
    AttemptIDs     []string
    ResultFactID   string
    CreatedAt      time.Time
    ClaimedAt      *time.Time
    ConcludedAt    *time.Time
    Error          string
}
```

- `bootstrap` 是保留 Intent：`from=[origin]`，用于直接尝试整个任务，只允许一个。
- `explore` Intent 来自 Reason；`from` 必须是已存在 Fact，不能包含 `goal`。
- `Intent.Description` 是方向，不是详细步骤；由 Explore prompt 展开。
- 同一 Intent 允许重试，但创建后不改变 `from`、`kind` 或 description。
- `claimed` 时 `Slot` 必须为 1 或 2，并对应固定 Executor。
- `concluded` 必须绑定一个 `ResultFactID`。
- `abandoned` 表示预算/执行/解析结果无法形成 Fact；它释放槽位，但保留历史和错误。
- Intent ID 由 controller 生成，Reason 不能指定 ID。

### 4.3 Hint

```go
type Hint struct {
    ID        string
    RunID     string
    Content   string
    Creator   string
    CreatedAt time.Time
}
```

- Hint 是可读上下文，不建立成功、不创建 Intent。
- 初始实现支持把 TSec hint 或调用方提供的只读提示写入 Hint；不提供交互式 hint API。
- Hint 出现在 Reason、Bootstrap 和 Explore 的 bounded context 中。

### 4.4 Attempt

```go
type AttemptPhase string

const (
    AttemptRunning    AttemptPhase = "running"
    AttemptConcluded  AttemptPhase = "concluded"
    AttemptFailed     AttemptPhase = "failed"
    AttemptCancelled  AttemptPhase = "cancelled"
    AttemptTimedOut   AttemptPhase = "timed_out"
)

type Attempt struct {
    ID           string
    RunID        string
    IntentID     string
    Slot         int
    SessionID    string
    Phase        AttemptPhase
    StartedAt    time.Time
    FinishedAt   time.Time
    Error        string
    Conclusion   string // execute | conclude | none
}
```

- 一个 Intent 对应多次 Attempt；建议默认最多 3 次。
- Execute 与 Conclude 属于同一个 Attempt 的两个阶段，不创建第二个 Attempt。
- 只有 controller 可以创建、认领、完成或放弃 Attempt。
- recoverable failure 后 Intent 可回到 open 并由原槽位立即重试；达到 `MaxAttemptsPerIntent` 后 abandoned。

### 4.5 ExploreReport

```go
type FactProposal struct {
    Kind           FactKind
    Description    string
    ObservationRefs []string
}

type ExploreReport struct {
    IntentID      string
    Phase         AttemptPhase
    Fact          *FactProposal
    Observations  []ObservationProposal
    Error         string
}
```

- `Fact` 为空时，controller 标记 Intent abandoned 或进入重试。
- `finding` Fact 必须有至少一个带 evidence file 的 Observation。
- `negative` Fact 可以无 Observation，但不能用于完成判定。
- ExploreReport 的 Fact.Kind 只允许 finding 或 negative。
- 移除 `AttemptReport.ChildBranches`、`AttemptReport.Knowledge`。

## 5. 控制循环

### 5.1 Bootstrap

```text
create origin + goal facts
create bootstrap intent from origin
claim slot-1
execute broad direct-solve prompt
-> Fact or abandoned
-> if backend completed: finalize
-> otherwise enter Reason
```

- Bootstrap 只使用一个槽位；另一个槽位在 Bootstrap 结束前保持空闲，避免双倍消耗在大范围尝试上。
- Bootstrap prompt 只看到 origin、goal、hints、target 和 attempt budget，不看到未发生的 frontier。
- Bootstrap 的完成仍然只能由 backend/Verifier 决定。

### 5.2 Reason

Reason 在以下条件运行：

- 至少一个槽位空闲；且
- 没有可立即认领的 open Intent；且
- 上次 Reason 后图发生了变化，或存在空闲槽位但没有 Intent。

Reason 输入：

- 完整 Fact 图（受条数/字节上限）；
- 最多两个 open/claimed Intent；
- hints；
- execution slot 状态；
- frontier version；
- 最近完成 Intent/Fact 摘要。

Reason 输出：

```go
type IntentProposal struct {
    From        []string `json:"from"`
    Description string   `json:"description"`
}

type GoalCandidate struct {
    From        []string `json:"from"`
    Description string   `json:"description"`
}

type ReasonReport struct {
    FrontierVersion uint64           `json:"frontier_version"`
    Intents         []IntentProposal `json:"intents"`
    GoalCandidate   *GoalCandidate   `json:"goal_candidate,omitempty"`
}
```

规则：

- 最多产生“空闲槽位数”个 Intent；多余项拒绝并写事件。
- 当空闲槽位存在且没有 open Intent 时，必须至少产生一个 Intent，或给出 goal candidate；二者不能同时出现。
- `GoalCandidate` 只触发 Verifier，不直接结束 Run。
- Reason turn 串行；同一时间只允许一个 Reason 调用。
- 返回时 frontier version 必须仍匹配；否则整体拒绝为 `stale_frontier`。

### 5.3 Explore

Explore 的输入：

- objective、target、hints；
- 当前完整 Fact/Intent 图的 bounded snapshot；
- 被认领的 Intent（`from` facts + description）；
- attempt budget；
- Agent-visible 工具与 tsec-submit skill。

Explore 的 execute prompt 要求：

1. 只推进当前 Intent；
2. 只访问授权 target；
3. 将可复核产出写入 workspace 文件；
4. 完成后输出结构化 `ExploreReport`；
5. 不以 agent 文本声明 Run 成功。

### 5.4 Conclude fallback

当 execute phase 超时、输出不可解析或没有产生 Fact，且 Agent session 仍存活时：

1. 不立即释放 Intent；
2. 在同一 Executor session 上发送 conclude prompt；
3. conclude prompt 明确要求：停止探索、停止等待、不得启动新扫描，只总结已经确认的事实；
4. 从仍有效的 Run context 派生独立 `conclude_timeout` context，但不继承已经超时的 execute context；
5. 解析结论并归档其引用的 evidence；
6. 成功则生成 Fact；否则释放/重试/abandon Intent。

若 Agent process 已退出或 session 已不可用，则不能进入 conclude fallback，只能按 Attempt failure 处理。

建议默认值：

```text
execute timeout: 300s
conclude timeout: 90s
max attempts per intent: 3
```

这直接解决当前基准中“执行 10 分钟但零 Observation、零 Fact”的问题。

## 6. 双槽调度

### 6.1 固定槽位

- `slot-1` 固定绑定 `executor-1`。
- `slot-2` 固定绑定 `executor-2`。
- `ValidateRunSpec` 要求恰好两个 Executor；`MaxSessions >= 3`（1 Reason Planner + 2 Executors）。
- 一个槽位同时最多一个 Attempt。
- Reason Planner 不占执行槽位，但同一时间只能运行一个 Reason turn。

### 6.2 不变量

```text
open_intents + claimed_intents <= 2
```

- Intent 创建后立即被空闲槽位 claim；不存在可见的 pending 队列。
- 若 Reason 返回两个 Intent 但只有一个空槽，只接受一个。
- 若 Reason 返回的 Intent 超过空槽数，多余项拒绝并写 `intent.rejected`，不会持久化为第三条。
- 已结束的 concluded/abandoned Intent 不占槽位。

### 6.3 补位

1. Intent claim 后，Attempt 在对应槽位执行。
2. Attempt 成功/失败并完成 Fact 归档后，controller 释放槽位。
3. 槽位释放后，controller 立即进入一次串行补位调度。
4. 如果另一个槽位仍在执行，补位只填当前空槽。
5. 如果两个槽位都空，Reason 可一次补两个，但仍串行返回和校验。
6. 不等待 round barrier；两个槽位独立推进。

### 6.4 并发安全

- Attempt/Reason goroutine 只返回不可变结果，不直接改 controller 状态。
- Controller 是唯一写者。
- 每次补位捕获 `frontier_version`；返回时若版本变化：
  - 放弃与已占用槽位冲突的 proposal；
  - 只接受仍存在空槽且版本匹配的 proposal；
  - 其余写 `stale_frontier` 拒绝事件。
- backend completion、取消和 canonical failure 可随时取消 Reason 和两个 Attempt。

## 7. 完成、停滞与终止

### 7.1 权威完成

- TSec backend 的 `Done && Success` 可立即终止。
- Verifier 成功只能在 Reason 给出 goal candidate 或图耗尽时运行。
- goal candidate 被验证失败后记录 `verification.rejected`；若预算仍够，Run 继续 Reason/Explore。
- 收到 goal candidate 后停止创建新 Intent；等 active Attempt 归并后运行 Verifier，避免证据在验证时仍在变化。backend completion 可立即打断等待。
- Reason 自身永远不能把 Run 标为 succeeded。

### 7.2 停滞

按“连续无进展的 Intent 结论数”判定：

```text
无新 positive Fact
且无新增 Observation/Artifact
且 backend progress 未变化
=> stagnant conclusion +1
```

达到 `StagnationRounds` 后：

- 停止创建新 Intent；
- 等待/取消 active Attempt；
- 若存在可验证事实，运行 Verifier；否则以 stagnated/failed 结束。

### 7.3 终止条件

- backend 完成；
- verifier success；
- wall/token/tool/cost 预算耗尽；
- 取消；
- canonical 写入失败；
- 无槽位、无 open/claimed Intent、Reason 不再产生 Intent 且没有 goal candidate；
- 连续 stagnation 达到上限。

## 8. 证据、Fact 与知识迁移

### 8.1 证据规则

- 保留现有 Artifact、Observation、路径校验、大小限制和脱敏。
- positive Fact 必须有 Observation/Artifact 引用。
- Fact 描述最多 2048 bytes，禁止包含 flag-like secret。
- 证据先于 Fact 写入；Fact 写入失败不得留下成功 Run。
- Fact 不写 flag 明文；flag submission 仍由 tsec-submit 完成，backend completion 是成功依据。

### 8.2 Knowledge 退场

- 新 Run 不再产生 Knowledge、contested、review branch 或跨 Run knowledge reuse。
- 旧 Knowledge 数据、事件和 UI 只读保留，供历史运行复盘。
- 合并后不再有新的 review branch 自动生成。
- 若后续需要跨 Run 记忆，另行设计，不进入本次控制循环。

## 9. 持久化与事件

### 9.1 SQLite schema v5

新增：

- `facts`
- `intents`
- `intent_sources`
- `hints`
- `exploration_attempts_v2`（`intent_id`、`slot`、`phase`、`session_id`、`conclusion`）
- `exploration_observations_v2`（`intent_id`、evidence 引用）
- `exploration_observation_artifacts_v2`

复用：

- `artifacts` 作为内容寻址字节存储与元数据表。

旧表：

- `exploration_branches`
- `exploration_attempts`
- `exploration_observations`
- `knowledge`
- `knowledge_sources`

保留，不删除、不改写；schema migration 只做 additive 变更。历史日志/事件行数不减。

### 9.2 事件

新运行只写：

```text
run.created
run.provisioned
run.phase
run.finalized
agent.session.started
agent.event
fact.created
hint.created
reason.started
reason.completed
reason.rejected
intent.created
intent.claimed
intent.released
intent.concluded
intent.abandoned
intent.rejected
attempt.started
attempt.concluded
attempt.failed
observation.created
artifact.stored
verification.completed
verification.rejected
authority.progress
authority.completed
authority.observe_error
```

旧 branch/knowledge 事件继续支持读取，但新运行不再写入。

## 10. Web 可视化

新运行详情页：

- **Fact/Intent 图**：Fact 为节点，Intent 为有向边；未完成 Intent 指向虚目标。
- **两个 Executor 槽位**：显示 `slot-1/2`、绑定 Executor、当前 Intent、Attempt 和剩余预算。
- **Reason 状态**：最近一次 Reason、触发原因、frontier version。
- **Facts**：可展开查看 Observation/Artifact 和来源 Intent。
- **Hints**：只读展示。
- **Verdict**：继续显示权威 Verdict，不把 Fact/Reason 完成当成功。

旧运行：

- 继续按 Branch/Knowledge 事件渲染历史视图；
- 新 reducer 不得因未知旧事件崩溃。

## 11. 配置与 API

`RunPolicy.Exploration` 收敛为：

```go
type ExplorationPolicy struct {
    MaxOpenIntents          int           // 固定为 2
    MaxTotalIntents         int           // 总生命周期上限
    MaxAttemptsPerIntent    int
    MaxToolCallsPerAttempt  int
    MaxWallTimePerAttempt   time.Duration
    ConcludeTimeout         time.Duration
    StagnationRounds        int
    MaxPlannerTools         int
    MaxPlannerWallTime      time.Duration
    BootstrapEnabled        bool
}
```

- 移除 `MaxDepth`、`MaxNodes`、`MaxChildrenPerBranch`、`MaxActiveBranches`。
- `MaxOpenIntents` 默认 2，只允许 1 或 2。
- `MaxTotalIntents` 防止图形无限增长，默认 20。
- `BootstrapEnabled` 默认 true。
- `MaxSessions` 至少 3。

新增只读 API：

- `GET /v1/runs/{id}/graph` → `{facts, intents, hints, slots, reason}`
- 保留 `GET /v1/runs/{id}/branches` 仅供旧数据。

## 12. 验收

- Bootstrap 在初始 Run 中先执行，之后才进入 Reason。
- Reason 返回 3 个 Intent 时最多接受空闲槽数，其余写拒绝事件。
- 任一时刻 `open + claimed <= 2`，race test 不出现第三条。
- slot-1 完成后另一槽仍在运行时，立即执行 Reason 并用 Reason 新 Intent 填充 slot-1。
- stale frontier version 的 proposal 被完整拒绝。
- Execute 超时且 session 仍存活时调用 Conclude fallback；能产出 Fact，且不继续运行工具。
- Agent process 已退出时不尝试复用不可用的 session。
- positive Fact 无 Observation 时拒绝写入。
- Reason goal candidate 不会被当作成功；Verifier/backend 仍是唯一权威。
- 新 Run 不写 branch/knowledge 事件。
- 旧 SQLite 运行可读，迁移前后事件/日志行数不减少。
- Web 同时正确渲染新旧运行，未知事件局部忽略。
- `go test ./...`、`go test -race ./...`、`go vet ./...`、`go build ./cmd/harness` 通过。
- `npm --prefix web run lint/typecheck/test/build` 通过。

## 13. 实施顺序

1. 新领域类型 + validation + 纯状态测试。
2. Fact/Intent store schema v5 + legacy read tests。
3. 双槽 controller 与 Reason 串行补位。
4. Bootstrap + Explore execute/conclude 两阶段。
5. 证据归档与 positive Fact 约束。
6. 事件、HTTP graph API、Web 新视图。
7. 旧数据渲染兼容与迁移文档。
8. 真实 TSec 冒烟与统计基准。

## 14. 留给 builder 的实现选择

- 双槽 loop 使用两个固定 goroutine + result channel，还是单 select loop；推荐单 select loop，controller 单写者。
- Web 是否引入 Cytoscape；若已有 React 组件能表达 DAG，优先不加新依赖。
- Reason prompt 的 JSON schema 与输出截断策略；推荐先复用现有 strict JSON parser。
