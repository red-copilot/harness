# 第一性原理进攻性安全通用智能体 SDK v1 设计

- 日期：2026-10-21
- 状态：Implemented
- 变更类型：公开 Go API 断代重构
- 取代：`2026-10-21-minimal-harness-architecture-design.md` 和 `2026-10-21-exploration-convergence-design.md` 中与新契约冲突的部分
- 影响面：根 `harness` 包、`pi`、`sandbox/docker`、`tsec`、`verify`、`store/sqlite`、`server`、CLI、Web、README 与架构文档

---

## 1. 意图

Red Harness v1 是面向安全 Agent 开发者的纯 Go SDK，而不是某个模型、某个 CLI 或某个竞赛平台的包装层。

它解决的问题是：

> 如何让一个不可信的 Agent，在单一授权目标上自主探索、执行 Shell 和积累证据，同时让分支、并发、预算、知识信任、审计与最终成功判定都由代码而不是 prompt 保证。

v1 的核心判断是：

- 推理循环由 Agent Runtime 负责，默认实现是 Pi。
- 探索控制由 SDK 负责，固定为可审计的假设树机制。
- 执行发生在可插拔 Sandbox 中，默认实现是 Docker。
- 网络隔离由调用方保证，并在 Run 输入中显式声明。
- 只有 `Verifier` 或 backend completion 可以产生成功 verdict。
- 安全领域语义不进入核心类型，由 skills、工具镜像和 adapter 提供。

## 2. 不可约约束

后续所有行为必须能追溯到一个或多个约束；没有强制点或测试的声明不得进入公开契约。

- **C1 成功不可伪造。** Agent 文本、Shell 退出码、Observation、Knowledge、`local_success`、探索停滞和 session 成功都不能建立 Run 成功；只有 `Verifier` 或 backend completion 可以。
- **C2 机械不变量归代码。** 分支因子、并发、深度、节点数、Attempt 次数、工具预算、上下文上界、信任转换和终止条件由 Go 强制；LLM 只提供语义内容。
- **C3 证据先于知识。** Knowledge 必须引用当前 Attempt 中已经归档的 Observation；Observation 必须引用已校验并归档的 Artifact。LLM 提供的 ID 不作为身份。
- **C4 审计不可变。** Event、Log、Branch、Attempt、Observation、Artifact 元数据和 Knowledge 历史只追加；迁移不得删除或重写既有审计行。
- **C5 失败关闭。** 任一 canonical 写入失败、malformed 控制结果、事件流提前关闭、清理失败或 verifier 错误都必须产生非成功终态。
- **C6 秘密不落盘。** 已知 secret 在事件、日志、规范化状态、Artifact、Result、Verdict 和错误落盘前脱敏；文本 Artifact 同样脱敏，二进制 Artifact 发现已知 secret 字节时拒绝归档。
- **C7 Agent 与 Shell 均不可信。** Agent 可以自由使用 Shell，但容器不得获得 Docker socket，必须保留只读根文件系统、`no-new-privileges`、能力最小化和资源限制。
- **C8 单控制语义。** 一个 Run 只有一条探索控制路径；没有 Workflow/direct-workflow 双路径，也没有新旧两套 frontier/exploration 状态机。
- **C9 单 Run 单 Target。** 一次 Run 只有一个逻辑 Target；Target 可以包含多个地址，但不引入 Mission、Campaign 或跨目标调度。
- **C10 无隐式恢复。** 进程重启后未完成 Run 只标记 `interrupted`；SDK 不提供 checkpoint、resume 或副作用重放。
- **C11 有界模型上下文。** Planner 与 Executor 看到的 Knowledge、Branch 和 Attempt 摘要必须有确定性的条数、字节和截断规则。
- **C12 每个声明状态必须可达。** 公开枚举值必须有至少一个代码转换和测试；否则不得出现在公开 API 中。

## 3. 目标、非目标与反目标

### 3.1 目标

1. 开发者可以使用纯 Go API 绑定一个 Planner、多个 Executor、一个 Sandbox Runtime、一个 Target Backend 和一个 Verifier。
2. 默认 Pi + Docker 组合可以开箱执行多轮探索，并允许用自定义 `AgentRuntimeFactory` 替换 Pi。
3. 分支树、批次并发、深度/节点预算、Attempt 预算、停滞判断和上下文摘要由 SDK 机械执行。
4. Knowledge 在同一 `Task.ID` 内支持受控跨 Run 复用，并拥有完整来源、信任、冲突和过期语义。
5. Attempt 证据文件成为内容寻址 Artifact；SQLite 元数据与其他审计记录构成可验证的 canonical 记录。
6. 公开 `Result` 同时返回权威 verdict 和完整探索快照，调用方可生成自己的安全报告。
7. CLI、HTTP、SSE 和 Web 只消费同一套引擎与事件，不拥有第二套控制逻辑。

### 3.2 非目标

- 不实现通用 LLM SDK、模型路由或上下文压缩框架。
- 不提供类型化 Tool/Capability Broker；Executor 的主能力是 Shell。
- 不在 SDK 内定义 Web、AD、云、凭据、漏洞或 MITRE 等领域本体。
- 不实现多 Mission、多 Target、团队协作、租户或长期 Project。
- 不实现 checkpoint、resume、副作用补偿或分布式 worker。
- 不在 v1 内做向量检索、RAG 或自动知识总结。
- 不提供旧公开 API 的长期兼容层；允许一次明确的 v1 断代。

### 3.3 反目标

- 关键限制仍只存在于 prompt 或 README。
- `MaxActiveBranches` 名为并发但实现串行。
- 一个节点能生成超过 `MaxChildrenPerBranch` 个假设子节点。
- Knowledge 可以引用未知 Observation，Observation 可以引用未归档文件。
- 新枚举值没有任何构造或转换路径。
- 迁移为了“清理”删除历史事件、日志或证据。
- Workflow、直接会话与探索树形成多条成功或控制路径。
- 为了跨语言协议牺牲 Go 领域模型；v1 不承诺语言中立网络协议。

## 4. 架构总览

```text
Go Library
  │
  ▼
Run Engine ───────────────────────────────┐
  ├─ lifecycle / budget / cancellation    │
  ├─ deterministic exploration scheduler  │
  ├─ PlannerStrategy / ExecutorStrategy   │
  └─ single-writer state merge             │
        │                                  │
        ├──────────────► Store ────────────┤ SQLite canonical audit
        │                ArtifactStore      │ content-addressed evidence
        │                                  │
        ├──────────────► AgentRuntimeFactory
        │                  └─ pi (default)
        │                                  │
        ├──────────────► Sandbox / Target Backend
        │                  ├─ docker        │
        │                  └─ tsec          │
        │                                  │
        └──────────────► Verifier ─────────┘ authoritative verdict
```

CLI、HTTP、SSE 和 Web 位于库之外，可以依赖根 `harness` 包，但根包不得依赖它们。

## 5. 包边界

### 5.1 根 `harness` 包

根包持有：

- 领域原语：Run、Task、Target、Branch、Attempt、Observation、Knowledge、Artifact、Verdict。
- 端口：`AgentRuntimeFactory`、`AgentSession`、`Backend`、`Verifier`、`Store`、`ArtifactStore`、`ProcessStarter`。
- 策略接口：`PlannerStrategy`、`ExecutorStrategy`。
- 固定探索引擎、状态机、预算、证据归档、知识转换和 redaction。
- 高层入口：`NewRunner`、`Runner.Start`、`Runner.Run`、`Run.Wait`、`Run.Cancel`、`Run.Steer`。

根包只依赖标准库。它不得导入 `pi`、Docker、TSec、SQLite、CLI、HTTP 或 Web。

### 5.2 适配器

- `pi`：实现 `AgentRuntimeFactory`，配置 Pi provider/model/tools，并严格使用 LF JSONL RPC。
- `sandbox/docker`：实现 `ProcessStarter`、`ResourceCleaner`。
- `tsec`：实现 target lifecycle `Backend` 和 TSec API client。
- `verify`：提供命令 verifier。
- `store/sqlite`：实现 `Store`，并提供本地内容寻址 `ArtifactStore`。
- `server`、`internal/cli`、`webui`：面向用户的适配层，只能调用公开 SDK 与 Store。

### 5.3 文件职责

具体文件名由实施计划固定；代码职责必须保持以下边界：

- 公开契约与端口不依赖控制实现。
- 探索调度与知识转换拆开，避免单文件继续膨胀。
- 证据路径校验、Artifact 归档与 redaction 不得散落在 adapter。
- Pi prompt、JSON 解析和 RPC 细节不得进入根包。

## 6. 领域模型

### 6.0 公共基础类型

```go
type Status string

const (
    StatusQueued       Status = "queued"
    StatusProvisioning Status = "provisioning"
    StatusRunning      Status = "running"
    StatusFinalizing   Status = "finalizing"
    StatusVerifying    Status = "verifying"
    StatusSucceeded    Status = "succeeded"
    StatusFailed       Status = "failed"
    StatusCancelled    Status = "cancelled"
    StatusTimedOut     Status = "timed_out"
    StatusInterrupted  Status = "interrupted"
    StatusStagnated    Status = "stagnated"
)

type Budget struct {
    WallTime      time.Duration `json:"wall_time,omitempty" yaml:"wall_time,omitempty"`
    MaxTokens     int           `json:"max_tokens,omitempty" yaml:"max_tokens,omitempty"`
    MaxModelCalls int           `json:"max_model_calls,omitempty" yaml:"max_model_calls,omitempty"`
    MaxToolCalls  int           `json:"max_tool_calls,omitempty" yaml:"max_tool_calls,omitempty"`
    MaxCostUSD    float64       `json:"max_cost_usd,omitempty" yaml:"max_cost_usd,omitempty"`
}

type Usage struct {
    InputTokens  int
    OutputTokens int
    TotalTokens  int
    ModelCalls   int
    ToolCalls    int
    CostUSD      float64
}

type ResourceSpec struct {
    CPUs      string `json:"cpus,omitempty" yaml:"cpus,omitempty"`
    Memory    string `json:"memory,omitempty" yaml:"memory,omitempty"`
    PidsLimit int    `json:"pids_limit,omitempty" yaml:"pids_limit,omitempty"`
}

type Completion struct {
    Done     bool
    Success  bool
    Progress int
    Total    int
    Score    float64
    Message  string
    Metadata map[string]string
}

type EventSink func(Event) error

type Config struct {
    Store         Store
    WorkspaceRoot string
    MaxSessions   int
    PollInterval  time.Duration
    CloseTimeout  time.Duration
    Logger        *slog.Logger
    Cleaner       ResourceCleaner
    SkipRecovery  bool
}

type Result struct {
    RunID        string
    Status       Status
    Verdict      *Verdict
    Branches     []Branch
    Attempts     []Attempt
    Observations []Observation
    Knowledge    []Knowledge
    Artifacts    []Artifact
    Sessions     []SessionResult
    Usage        Usage
    Error        string
    StartedAt    *time.Time
    FinishedAt   *time.Time
}
```

- `Config.MaxSessions` 默认 4，`PollInterval` 默认 500ms，`CloseTimeout` 默认 10s；非法负值由 `NewRunner` 拒绝。
- `slog.Logger` 可以为 nil；nil 时使用只写 stderr 的 warning logger。
- `Completion` 只有来自 `Backend.Observe` 的返回值可以进入权威完成判定。

### 6.1 Task 与 Target

```go
type Mount struct {
    Source      string `json:"source" yaml:"source"`
    Destination string `json:"destination" yaml:"destination"`
    ReadOnly    bool   `json:"read_only,omitempty" yaml:"read_only,omitempty"`
}

type Task struct {
    ID        string            `json:"id" yaml:"id"`
    Objective string            `json:"objective" yaml:"objective"`
    Inputs    []Mount           `json:"inputs,omitempty" yaml:"inputs,omitempty"`
    Skills    []Mount           `json:"skills,omitempty" yaml:"skills,omitempty"`
    Metadata  map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

type IsolationMode string

const IsolationCallerManaged IsolationMode = "caller_managed"

type Target struct {
    ID        string            `json:"id" yaml:"id"`
    Addresses []string          `json:"addresses,omitempty" yaml:"addresses,omitempty"`
    Isolation IsolationMode     `json:"isolation" yaml:"isolation"`
    Metadata  map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}
```

- `Task.ID` 是知识跨 Run 复用的硬边界，必须非空。
- 一个 Run 只有一个逻辑 Target；`Addresses` 表示该 Target 的多个入口。
- `Isolation` 在 v1 只允许 `caller_managed`。调用方必须在启动 Run 前保证网络只能到达授权 Target 与必要控制端。
- SDK 不声称实现网络 ACL；它记录该信任假设、拒绝 planner/executor 报告中的越界 Target 引用，并在 README 和架构文档中明确该限制。
- 如果使用 `Backend`，`Provision` 返回的 Target 必须保留 `caller_managed` 语义；无法满足时 Run 在 provisioning 阶段失败。

### 6.2 AgentSpec 与 Runtime

```go
type AgentSpec struct {
    ID            string              `json:"id" yaml:"id"`
    Image         string              `json:"image,omitempty" yaml:"image,omitempty"`
    Command       []string            `json:"command,omitempty" yaml:"command,omitempty"`
    Env           map[string]string   `json:"env,omitempty" yaml:"env,omitempty"`
    EnvFrom       []string            `json:"env_from,omitempty" yaml:"env_from,omitempty"`
    Mounts        []Mount             `json:"mounts,omitempty" yaml:"mounts,omitempty"`
    Skills        []Mount             `json:"skills,omitempty" yaml:"skills,omitempty"`
    Network       string              `json:"network,omitempty" yaml:"network,omitempty"`
    CapAdd        []string            `json:"cap_add,omitempty" yaml:"cap_add,omitempty"`
    Resources     ResourceSpec        `json:"resources,omitempty" yaml:"resources,omitempty"`
    RuntimeConfig json.RawMessage     `json:"runtime,omitempty" yaml:"runtime,omitempty"`
    Runtime       AgentRuntimeFactory `json:"-" yaml:"-"`
}
```

- 根包不出现 `PiSpec`、provider、model、thinking 或 Pi tool 名称。
- `RuntimeConfig` 由具体 runtime 解释；无效配置必须 fail-closed。
- Planner 仅获得只读 Agent 能力；Executor 可以获得完整 Shell。默认 Pi factory 根据 `RuntimeRequest.Role` 强制该差异。
- 自定义 runtime 必须实现同一 `AgentSession` 协议；SDK 不信任 runtime 返回的“成功”。

```go
type AgentRole string

const (
    AgentRolePlanner  AgentRole = "planner"
    AgentRoleExecutor AgentRole = "executor"
)

type AgentRuntimeFactory interface {
    NewSession(context.Context, RuntimeRequest) (AgentSession, error)
}

type RuntimeRequest struct {
    RunID        string
    SessionID    string
    Role         AgentRole
    Agent        AgentSpec
    Task         Task
    Target       Target
    WorkspaceDir string
    TaskDir      string
    SkillsDir    string
    Env          map[string]string
    Starter      ProcessStarter
}

type AgentSession interface {
    Prompt(context.Context, string) error
    Steer(context.Context, string) error
    Reset(context.Context) error
    Events() <-chan AgentEvent
    Logs() <-chan LogEvent
    Usage() Usage
    Abort(context.Context) error
    Close(context.Context) error
}

type AgentEvent struct {
    Type string
    Data json.RawMessage
}

type LogEvent struct {
    Stream string
    Text   string
}
```

- `AgentSession` 表示一个可多次重置的 runtime 进程，不表示“一次 turn”。
- turn 完成由核心消费 `agent_settled` 事件判断；`Wait` 不再属于核心 session 契约。
- `agent_end` 不是完成；事件流关闭且未观察到 `agent_settled` 视为 turn 失败。
- Planner 和 Executor 的 Session 生命周期由 Run Engine 独占管理。

### 6.3 RunSpec 与 RunDescriptor

```go
type RunSpec struct {
    Task             Task
    Target           Target
    Planner          AgentSpec
    Executors        []AgentSpec
    Policy           ExplorationPolicy
    Budget           Budget
    Backend          Backend
    Verifier         Verifier
    Artifacts        ArtifactStore
    SecretEnv        []string
    WorkspaceRoot    string
    PlannerStrategy  PlannerStrategy
    ExecutorStrategy ExecutorStrategy
}

type RunDescriptor struct {
    SchemaVersion int               `json:"schema_version"`
    Task          Task              `json:"task"`
    Target        Target            `json:"target"`
    Planner       AgentSpec         `json:"planner"`
    Executors     []AgentSpec       `json:"executors"`
    Policy        ExplorationPolicy `json:"policy"`
    Budget        Budget            `json:"budget"`
}
```

- `RunSpec` 是运行时绑定，包含接口字段。
- `RunDescriptor` 是可序列化审计与 CLI/HTTP 输入；接口字段通过 `json:"-"` 排除。
- `PlannerStrategy` 或 `ExecutorStrategy` 为 nil 时使用内置 session strategy；自定义 strategy 只能改变 prompt 与解析，不得绕过引擎校验。
- `RunSpec.Artifacts` 必须非 nil；SQLite 适配器同时实现 `Store` 与 `ArtifactStore` 是推荐组合。

### 6.4 Planner/Executor 策略

```go
type TurnResult struct {
    AssistantText string
    ToolCalls     []string
    Usage         Usage
}

type PlannerStrategy interface {
    Prompt(PlanRequest) (string, error)
    Parse(PlanRequest, TurnResult) (PlanProposal, error)
}

type ExecutorStrategy interface {
    Prompt(AttemptRequest) (string, error)
    Parse(AttemptRequest, TurnResult) (AttemptReport, error)
}

type TurnLimits struct {
    MaxToolCalls int
    ToolTimeout  time.Duration
}

type AttemptBudget struct {
    MaxAttempts  int
    MaxToolCalls int
    MaxWallTime  time.Duration
}
```

- 策略只生成 prompt 和解析 turn 输出。
- session reset、工具上限、wall time、事件持久化、报告大小、证据归档、分支/知识校验均由引擎执行。
- 默认 Executor strategy 要求 agent 写 `/workspace/report.json`，路径固定且每次 Attempt 前删除旧文件。

### 6.5 Branch、Attempt、Observation

```go
type BranchKind string

const (
    BranchRoot       BranchKind = "root"
    BranchHypothesis BranchKind = "hypothesis"
    BranchReview     BranchKind = "review"
)

type BranchStatus string

const (
    BranchOpen      BranchStatus = "open"
    BranchActive    BranchStatus = "active"
    BranchPaused    BranchStatus = "paused"
    BranchResolved  BranchStatus = "resolved"
    BranchRefuted   BranchStatus = "refuted"
    BranchBlocked   BranchStatus = "blocked"
    BranchAbandoned BranchStatus = "abandoned"
)

type Branch struct {
    ID               string
    ParentID         string
    RunID            string
    TaskID           string
    Kind             BranchKind
    Depth            int
    Topic            string
    Goal             string
    Hypothesis       string
    SuccessCondition string
    FailureCondition string
    Scope            string
    ReviewOf         []string
    Status           BranchStatus
    Priority         int
    CreatedAt        time.Time
    UpdatedAt        time.Time
}

type AttemptStatus string

const (
    AttemptRunning   AttemptStatus = "running"
    AttemptCompleted AttemptStatus = "completed"
    AttemptTruncated AttemptStatus = "truncated"
    AttemptFailed    AttemptStatus = "failed"
    AttemptCancelled AttemptStatus = "cancelled"
)

type AttemptOutcome string

const (
    AttemptLocalSuccess AttemptOutcome = "local_success"
    AttemptRefuted      AttemptOutcome = "refuted"
    AttemptBlocked      AttemptOutcome = "blocked"
    AttemptInconclusive AttemptOutcome = "inconclusive"
    AttemptFailure      AttemptOutcome = "failure"
)

type Attempt struct {
    ID             string
    RunID          string
    BranchID       string
    SessionID      string
    Status         AttemptStatus
    Outcome        AttemptOutcome
    ObservationIDs []string
    StartedAt      time.Time
    FinishedAt     time.Time
    Error          string
}

type ObservationProposal struct {
    LocalID       string   `json:"local_id"`
    Kind          string   `json:"kind"`
    Summary       string   `json:"summary"`
    Target        string   `json:"target"`
    EvidenceFiles []string `json:"evidence_files"`
}

type Observation struct {
    ID        string
    RunID     string
    BranchID  string
    AttemptID string
    Kind      string
    Summary   string
    Target    string
    Evidence  []Artifact
    CreatedAt time.Time
}

type BranchSummary struct {
    ID         string
    ParentID   string
    Kind       BranchKind
    Depth      int
    Topic      string
    Hypothesis string
    Scope      string
    Status     BranchStatus
    Priority   int
    ReviewOf   []string
}

type AttemptSummary struct {
    ID       string
    BranchID string
    Status   AttemptStatus
    Outcome  AttemptOutcome
    Error    string
}
```

- `Observation.Kind` 是 adapter 定义的命名空间字符串，不在核心枚举。
- `EvidenceFiles` 是 workspace-relative 路径；agent 不能提供 Artifact ID 或伪造哈希。
- `Observation.ID`、`Branch.ID`、`Attempt.ID` 均由代码生成，带 Run 作用域，保证多 Run 共库唯一。
- 每次 Attempt 最多归档 `MaxEvidenceFilesPerAttempt` 个文件、单文件 `MaxArtifactBytes`、总计 `MaxAttemptArtifactBytes`。
- 证据路径必须是 workspace 内普通文件；拒绝绝对路径、`..`、symlink escape 和目录。

### 6.6 AttemptReport

```go
type BranchProposal struct {
    ParentID         string `json:"parent_id"`
    Topic            string `json:"topic"`
    Goal             string `json:"goal"`
    Hypothesis       string `json:"hypothesis"`
    SuccessCondition string `json:"success_condition"`
    FailureCondition string `json:"failure_condition"`
    Target           string `json:"target"`
    Priority         int    `json:"priority,omitempty"`
}

type AttemptReport struct {
    BranchID       string                `json:"branch_id"`
    Status         AttemptStatus         `json:"status"`
    Outcome        AttemptOutcome        `json:"outcome"`
    Observations   []ObservationProposal `json:"observations"`
    Knowledge      []KnowledgeProposal   `json:"knowledge"`
    ChildBranches  []BranchProposal      `json:"child_branches"`
    Error          string                `json:"error,omitempty"`
}
```

```go
type PlanRequest struct {
    Task       Task
    Target     Target
    Branches   []BranchSummary
    Knowledge  []Knowledge
    Progress   Completion
    LastReports []AttemptSummary
}

type PlanProposal struct {
    CreateBranches []BranchProposal `json:"create_branches"`
    CloseBranches  []string         `json:"close_branches,omitempty"`
    Priorities     map[string]int   `json:"priorities,omitempty"`
}

type AttemptRequest struct {
    Task          Task
    Target        Target
    Branch        Branch
    Knowledge     []Knowledge
    Budget        AttemptBudget
    WorkspaceDir  string
}
```

- Planner 只能创建 `ParentID == root ID` 的 hypothesis；任何非 root 提案被拒绝并记录事件。
- Executor 仅当 `AttemptStatus == completed`、`Outcome == local_success` 且至少有一个成功归档的 Observation 时，才能创建 hypothesis 子分支。
- 每个 hypothesis 分支最多创建 `MaxChildrenPerBranch` 个子节点，默认 2；root 与 review 分支不受该数量限制，但仍受 `MaxDepth` 和 `MaxNodes`。
- Review 分支必须通过 `ReviewOf` 精确绑定两个冲突 Knowledge ID；只有同时支持一方、反驳另一方的报告能解决冲突。

### 6.7 权威端口与事件信封

```go
type ProcessSpec struct {
    Image        string
    Command      []string
    Env          map[string]string
    Mounts       []Mount
    Network      string
    CapAdd       []string
    Name         string
    Labels       map[string]string
    WorkingDir   string
    User         string
    ReadOnlyRoot bool
    PidsLimit    int
    Memory       string
    CPUs         string
}

type Process interface {
    Stdin() io.WriteCloser
    Stdout() io.Reader
    Stderr() io.Reader
    Wait(context.Context) (ExitStatus, error)
    Kill(context.Context) error
}

type ExitStatus struct {
    Code int
}

type ProcessStarter interface {
    Start(context.Context, ProcessSpec) (Process, error)
}

type ResourceCleaner interface {
    CleanupOrphans(context.Context) error
}

type ProvisionRequest struct {
    RunID    string
    Task     Task
    Target   Target
    Metadata map[string]string
}

type Backend interface {
    Provision(context.Context, ProvisionRequest) (Target, error)
    Observe(context.Context, Target) (Completion, error)
    Close(context.Context, Target) error
}

type VerifyRequest struct {
    RunID         string
    Task          Task
    Target        Target
    Sessions      []SessionResult
    WorkspaceRoot string
    Store         Store
}

type Verifier interface {
    Verify(context.Context, VerifyRequest) (Verdict, error)
}

type Verdict struct {
    Success  bool
    Score    float64
    Message  string
    Evidence json.RawMessage
}

type Event struct {
    Seq           int64
    SchemaVersion int
    RunID         string
    SessionID     string
    Source        string
    Type          string
    Time          time.Time
    Data          json.RawMessage
    Raw           json.RawMessage
}

type LogRecord struct {
    Seq       int64
    RunID     string
    SessionID string
    Stream    string
    Time      time.Time
    Text      string
}

type SessionResult struct {
    ID       string
    Status   Status
    ExitCode int
    Usage    Usage
    Error    string
}
```

- `Event.SchemaVersion` 在本设计中固定为 1；写入者不得省略。
- `Verifier` 返回错误表示验证基础设施失败；返回 `Verdict{Success:false}` 表示验证完成但未通过。
- Backend completion 和 Verifier 是唯一两个可以从 `Result` 建立成功状态的来源。

## 7. 探索控制循环

### 7.1 默认策略

```go
type ExplorationPolicy struct {
    MaxDepth                   int
    MaxNodes                   int
    MaxChildrenPerBranch       int
    MaxActiveBranches          int
    MaxAttemptsPerBranch       int
    MaxToolCallsPerAttempt     int
    MaxWallTimePerAttempt      time.Duration
    StagnationRounds           int
    MaxPlannerTools            int
    MaxPlannerWallTime         time.Duration
    MaxKnowledgeItems          int
    MaxKnowledgeStatementBytes int
    MaxKnowledgeDigestItems    int
    MaxKnowledgeDigestBytes    int
    MaxReportBytes             int64
    MaxEvidenceFilesPerAttempt int
    MaxArtifactBytes           int64
    MaxAttemptArtifactBytes    int64
}
```

| 参数 | 默认值 |
|---|---:|
| `MaxDepth` | 4 |
| `MaxNodes` | 15 |
| `MaxChildrenPerBranch` | 2 |
| `MaxActiveBranches` | 2 |
| `MaxAttemptsPerBranch` | 3 |
| `MaxToolCallsPerAttempt` | 20 |
| `MaxWallTimePerAttempt` | 10m |
| `StagnationRounds` | 3 |
| `MaxPlannerTools` | 4 |
| `MaxPlannerWallTime` | 2m |
| `MaxKnowledgeItems` | 200 |
| `MaxKnowledgeStatementBytes` | 200 |
| `MaxKnowledgeDigestItems` | 8 |
| `MaxKnowledgeDigestBytes` | 2048 |
| `MaxReportBytes` | 1 MiB |
| `MaxEvidenceFilesPerAttempt` | 16 |
| `MaxArtifactBytes` | 10 MiB |
| `MaxAttemptArtifactBytes` | 50 MiB |

`MaxActiveBranches` 是同时在飞的 Attempt 上限，不是每轮选择数；实际并发还受 Executor 数量和 `Config.MaxSessions` 约束。

### 7.2 轮次屏障

```text
load reusable knowledge
create root branch
while run active and tree not exhausted:
    if authoritative completion done: stop
    if planning needed: reset planner; request root-level branches
    ready = deterministic ready-branch ordering
    batch = first MaxActiveBranches branches
    run batch concurrently, one executor per attempt
    wait for all batch attempts
    single-writer collector validates and merges in deterministic branch order
    apply attempts, observations, artifacts, knowledge and children
    update stagnation
```

- 不在同一轮内持续补位；每轮结束后再规划。
- 所有共享切片和 Store 写入只发生在 collector goroutine。
- 并发 goroutine 只执行 reset、prompt/event turn、读取 report 和返回不可变结果。
- 一个 Executor session 在一个 Batch 中最多服务一个 Attempt；同一分支重试时先 reset。

### 7.3 终止

Run 进入终态的条件：

- backend completion 返回 `Done=true`：以 backend 权威结果终止。
- 所有分支耗尽或停滞：若存在 Verifier，则进入 verifying；否则以 `stagnated` 终止。
- 探索正常结束且无权威来源：`failed`。
- wall time、模型/工具/成本预算超限：`failed` 或 `timed_out`。
- 调用方取消：`cancelled`。
- Store/Artifact/规范状态写入失败：`failed`。
- Verifier 错误或明确失败：`failed`，成功时 `succeeded`。

### 7.4 上下文上界

- Knowledge 摘要先过滤 scope/reuse/expiry，再按 `corroborated > observed > contested`、`ValidFrom` 降序、ID 升序排序。
- 最多注入 8 条；每条 statement UTF-8 安全截断到 200 字节；总计不超过 2048 字节。
- refuted 和过期条目永不注入。
- Branch 摘要字段分别截断到固定上界，且总节点数由 `MaxNodes` 限制。
- 只注入最近一个 Batch 的 Attempt 摘要，数量不超过 `MaxActiveBranches`。
- 发生截断时写入 `exploration.context.truncated`，包含上下文类型、候选数、保留数和丢弃数。

## 8. 证据与 Artifact

```go
type Artifact struct {
    ID         string
    RunID      string
    AttemptID  string
    SessionID  string
    Name       string
    MediaType  string
    Size       int64
    SHA256     string
    Redacted   bool
    CreatedAt  time.Time
}

type ArtifactInput struct {
    RunID     string
    AttemptID string
    SessionID string
    Name      string
    MediaType string
    Size      int64
    Content   io.Reader
}

type ArtifactStore interface {
    Put(context.Context, ArtifactInput) (Artifact, error)
    Open(context.Context, string) (io.ReadCloser, error)
}
```

- Artifact ID 为存储内容的 SHA-256；相同 Run 内相同内容可去重，但保留独立来源关系。
- 读取不得超过声明 size；声明与实际不一致即失败。
- 文本内容（合法 UTF-8）在存储前应用 Redactor；文本 redaction 后计算哈希。
- 二进制内容扫描已知 secret 原文；命中时拒绝归档。
- SQLite Store 保存 Artifact 元数据及与 Observation 的引用关系；默认 Artifact 内容保存在 `.harness/artifacts/sha256/`。
- Workspace 仍是运行 artifact；只有被 AttemptReport 显式引用且归档成功的文件才能成为 canonical evidence。

## 9. Knowledge 与信任

### 9.1 类型

```go
type KnowledgeKind string // adapter-defined namespace string

type KnowledgeScope string

const (
    KnowledgeScopeTask     KnowledgeScope = "task"
    KnowledgeScopeTarget   KnowledgeScope = "target"
    KnowledgeScopeInstance KnowledgeScope = "instance"
)

type KnowledgeTrust string

const (
    KnowledgeObserved     KnowledgeTrust = "observed"
    KnowledgeCorroborated KnowledgeTrust = "corroborated"
    KnowledgeContested    KnowledgeTrust = "contested"
    KnowledgeRefuted      KnowledgeTrust = "refuted"
)

type ReusePolicy string

const (
    ReuseStable   ReusePolicy = "stable"
    ReuseInstance ReusePolicy = "instance"
    ReuseNever    ReusePolicy = "never"
)

type KnowledgeProposal struct {
    Topic           string         `json:"topic"`
    Kind            KnowledgeKind  `json:"kind"`
    Statement       string         `json:"statement"`
    Scope           KnowledgeScope `json:"scope"`
    Target          string         `json:"target,omitempty"`
    ObservationRefs []string       `json:"observation_refs"`
    Supports        []string       `json:"supports,omitempty"`
    Refutes         []string       `json:"refutes,omitempty"`
    Reuse           ReusePolicy    `json:"reuse"`
    ExpiresAt       *time.Time     `json:"expires_at,omitempty"`
}

type KnowledgeSource struct {
    RunID       string
    AttemptID   string
    SessionID   string
    Observation string
}

type Knowledge struct {
    ID            string
    TaskID        string
    RunID         string
    Topic         string
    Kind          KnowledgeKind
    Statement     string
    Scope         KnowledgeScope
    Target        string
    Trust         KnowledgeTrust
    Reuse         ReusePolicy
    Sources       []KnowledgeSource
    Contradicts   []string
    ValidFrom     time.Time
    LastValidated time.Time
    ExpiresAt     *time.Time
    Version       int64
}
```

### 9.2 转换

- 新 Knowledge：`observed`；必须引用同次 Attempt 已归档 Observation，并由代码生成 ID。
- 引用已有 Knowledge 的 `Supports`：追加当前 source；来自至少两个不同 Executor Session ID 时转为 `corroborated`。
- 引用已有 Knowledge 的 `Refutes`：双方转为 `contested`，并创建精确绑定两个 ID 的 review 分支。
- Review 报告必须支持一方且反驳另一方；胜方转为 `corroborated`，败方转为 `refuted`，并追加来源。
- 不提供 `verified` Knowledge。Verifier 只裁决 Run，不提升知识信任。
- `refuted` 是当前 Task 范围内的终态；新的 Run 可以创建新条目，但不得把 refuted 条目重新注入模型上下文。

### 9.3 跨 Run 复用

```go
type KnowledgeQuery struct {
    TaskID            string
    RunID             string
    TargetFingerprint string
    AllowCrossRun     bool
    Now               time.Time
}
```

- `TaskID` 永远是硬边界。
- `ReuseNever` 仅同 Run；`ReuseInstance` 仅同 Target fingerprint；`ReuseStable` 可跨 Run。
- `Scope=task` 在同一 Task 内可见；`Scope=target` 要求 Target 匹配；`Scope=instance` 要求 Target 匹配且未过期。
- `ExpiresAt` 非零且已过期时不返回，并记录 `knowledge.expired` 事件。
- refuted 永不返回；contested 可以返回但必须标记。
- Target fingerprint 由已解析 Target 的 canonical ID、排序后的 addresses 和显式 metadata 键计算；不包含地址顺序或非稳定运行时字段。

### 9.4 Knowledge 上界

- 单 Run 最多加载和产生 `MaxKnowledgeItems` 条；超限拒绝新提案并记录事件。
- Topic、Statement、Target 和来源数量均有硬上限。
- 重复指纹由 `(TaskID, Topic normalized, Scope, Kind, TargetFingerprint)` 决定；代码拒绝重复而非合并未经验证的自由文本。
- 所有 Knowledge 写入必须在 central redacting store 后完成；持久化失败终止 Run。

## 10. Run 状态机与失败语义

```text
queued
  -> provisioning
  -> running
  -> finalizing
  -> verifying (optional)
  -> succeeded | failed | cancelled | timed_out | stagnated | interrupted
```

- `provisioning`：建立 Target、workspace、skills、runtime sessions。
- `running`：探索循环和 backend completion 竞争。
- `finalizing`：停止所有生产者、关闭 session/target、汇总 Usage 和结果。
- `verifying`：调用 Verifier；只有其返回的 `Verdict` 能产生成功。
- 终态写入失败时，内存 Result 必须降级为 failed，不能返回成功。
- 事件 channel 只能由其所有生产者全部停止后关闭。
- 任何 Store/ArtifactStore/规范状态错误都先取消 context，再等待生产者退出，最后写 failed 终态。
- Backend completion 到达时立即取消在飞 Attempt；已产生的 Observation/Artifact 保留审计，但不能覆盖权威结果。
- Run 不实现 resume；启动时 `MarkInterrupted` 将所有非终态历史 Run 标记 `interrupted`。

## 11. 执行与安全边界

### 11.1 Sandbox

- 根包只依赖 `ProcessStarter` 与 Sandbox spec；Docker adapter 默认实现。
- 默认容器：只读根文件系统、`no-new-privileges`、drop 全部 capabilities 后按需添加、CPU/内存/PID 限制、无 Docker socket、只挂载 Task/Skills/Workspace。
- `NET_RAW` 只作为显式 Executor capability；Planner 默认为只读工具，无 Shell。
- sandbox 启动失败、进程提前退出、事件流关闭或清理失败都是 fail-closed。
- Runtime factory 不得自行绕过 `ProcessStarter` 创建容器；测试用 in-memory starter/factory 可以替代。

### 11.2 网络

- v1 仅支持 `caller_managed` 隔离。
- SDK 校验 Branch/Attempt/Knowledge 中声明的 Target 必须匹配运行 Target 的 ID 或地址。
- SDK 不声称阻止 Shell 访问其他地址；这一保证由调用方提供的网络边界承担。
- README、CLI help 和 HTTP schema 必须明确该限制，不能把语义校验描述为网络隔离。

### 11.3 Pi

- Pi 仍是默认 Agent Runtime，不是根契约的一部分。
- Pi 使用严格 LF JSONL；`agent_settled` 是 turn 完成，`agent_end` 不是。
- Planner 的 Pi `RuntimeConfig` 默认 tools=`read`；Executor 默认 tools=`read,bash,edit,write`。
- Pi 的 provider/model/thinking/tools/env passthrough/skills/image 配置移入 `pi.Config`，由 `pi` 包解释。
- Pi 的 RPC malformed line、命令错误、提前退出和 event channel 关闭均保留为审计事件并使当前 turn fail-closed。

### 11.4 TSec

- TSec 是 `Backend` adapter，不作为核心概念。
- TSec 负责 challenge start/close/completion polling；flag 提交仍可由 Executor skill 调用。
- `BENCHMARK_TOKEN` 是显式凭据例外：可进入 agent 容器，但必须由 Redactor 在所有持久化路径中移除。
- 文档必须说明容器本身属于凭据信任边界。

## 12. 持久化

### 12.1 Store 契约

```go
type RunRecord struct {
    ID         string
    Status     Status
    Spec       json.RawMessage
    Result     json.RawMessage
    Error      string
    SweepID    string
    Metadata   json.RawMessage
    CreatedAt  time.Time
    StartedAt  *time.Time
    FinishedAt *time.Time
}

type VerdictRecord struct {
    RunID     string
    Success   bool
    Score     float64
    Message   string
    Evidence  json.RawMessage
    CreatedAt time.Time
}

type SweepRecord struct {
    ID         string
    Status     Status
    Spec       json.RawMessage
    Result     json.RawMessage
    Error      string
    CreatedAt  time.Time
    FinishedAt *time.Time
}

type Store interface {
    CreateRun(context.Context, RunRecord) error
    UpdateRun(context.Context, RunRecord) error
    GetRun(context.Context, string) (RunRecord, error)
    ListRuns(context.Context, int) ([]RunRecord, error)

    AppendEvent(context.Context, Event) (Event, error)
    ListEvents(context.Context, string, int64) ([]Event, error)
    AppendLog(context.Context, LogRecord) (LogRecord, error)
    ListLogs(context.Context, string, int64) ([]LogRecord, error)

    SaveVerdict(context.Context, VerdictRecord) error
    GetVerdict(context.Context, string) (VerdictRecord, error)

    SaveBranch(context.Context, Branch) error
    ListBranches(context.Context, string) ([]Branch, error)
    SaveAttempt(context.Context, Attempt) error
    ListAttempts(context.Context, string) ([]Attempt, error)
    SaveObservation(context.Context, Observation) error
    ListObservations(context.Context, string) ([]Observation, error)
    SaveArtifact(context.Context, Artifact) error
    ListArtifacts(context.Context, string) ([]Artifact, error)

    SaveKnowledge(context.Context, Knowledge) error
    UpdateKnowledge(context.Context, Knowledge) error
    QueryKnowledge(context.Context, KnowledgeQuery) ([]Knowledge, error)

    SaveSweep(context.Context, SweepRecord) error
    GetSweep(context.Context, string) (SweepRecord, error)
    ListSweeps(context.Context, int) ([]SweepRecord, error)
    MarkInterrupted(context.Context) error
    Close() error
}
```

- 所有写操作都经过 `redactingStore`/`redactingArtifactStore` 包装；adapter 不自行决定 redaction。
- `AppendEvent` 和 `AppendLog` 采用数据库序列分配；SSE 依靠 seq replay。
- Store 迁移只能创建/添加列和表，不能 `DELETE` 历史 event/log。
- normalized 表主键必须包含 Run 作用域，保证多 Run 共库不冲突。
- Knowledge 表使用追加版本；更新 trust/contradiction 时写新 version，不覆盖历史来源。
- `MarkInterrupted` 只改变 Run 终态，不删除已写入事件。

### 12.2 Event 与 Result

- `Event` 增加 `SchemaVersion int`；v1 事件固定为 1。
- 事件类型使用稳定命名空间：`run.*`、`agent.*`、`exploration.*`、`knowledge.*`、`artifact.*`、`verification.*`。
- Unknown event type 在 UI reducer 中逐条忽略，不导致整个 Run 视图失败。
- `Result` 包含 verdict、branches、attempts、observations、knowledge、artifacts、sessions、usage、error 和时间。
- Result 中的 exploration 数据是非权威快照；只有 `Verdict` 表示成功。

### 12.3 Server 与 Web

- HTTP API 接受可序列化 `RunDescriptor` 加上由 deployment 配置的 adapter 绑定。
- 服务仍可保持本地单用户、无认证；若暴露到网络，认证和 TLS 由外部边界负责。
- Web 只读展示 Run、事件、日志、探索树、知识、Artifact 元数据和 verdict。
- UI 的 `exploration.*` reducer 只依赖事件契约，不依赖 agent 文本。
- CLI YAML/JSON 只描述 `RunDescriptor`；runtime/backend/verifier/artifact store 由 CLI composition 注入。

## 13. 迁移影响

| 旧 API/行为 | v1 |
|---|---|
| `RunSpec.Agents []AgentSpec` | `RunSpec.Planner` + `RunSpec.Executors` |
| `RunSpec.Workflow` / `WorkflowRuntime` | 删除；固定探索引擎 |
| `exploration.Configure` | 删除；`RunSpec.Policy` 直接配置 |
| `harness.PiSpec` | 删除；Pi 配置进入 `pi.Config` / `AgentSpec.RuntimeConfig` |
| `Task.Targets []Target` | 删除；Run 只有一个 `Target` |
| `AgentSession.Wait` 作为 turn 完成 | 删除；核心消费 `agent_settled` event |
| `KnowledgeVerified` | 删除；最高信任为 `corroborated` |
| `KnowledgeKind` 固定安全枚举 | 改为 adapter 命名空间字符串 |
| `Observation.EvidenceRefs []string` | Agent 提交 `EvidenceFiles`；代码归档后生成 Artifact 引用 |
| 可选 `WorkflowConfig` | `RunDescriptor` |
| 独立 exploration state v1/v2 | 删除；唯一固定探索引擎 |
| `AgentSessionFactory` 别名 | 改为 runtime-neutral `AgentRuntimeFactory` |

迁移指南必须提供旧 `RunSpec` 到新 composition 的代码示例、Pi 配置迁移、TSec 与 CLI 配置映射，以及旧 DB 升级说明。

## 14. 验收标准

### 14.1 机械不变量

- 三个子分支提案只创建两个，额外提案产生 rejection 事件。
- 非 `local_success` 或没有 Observation 的 Attempt 创建零个子分支。
- Planner 的非 root 提案全部拒绝。
- `MaxActiveBranches=2` 时，barrier test 证明两个 Attempt 同时处于 running。
- `MaxDepth` 与 `MaxNodes` 独立生效；root/review 的规则有专门测试。
- Planner/Executor 摘要满足条数、单条和总字节上限。
- refuted、过期、错误 scope 和错误 reuse 的知识不进入上下文。

### 14.2 证据与知识

- 路径穿越、symlink escape、目录、超限文件、声明 size/hash 不一致均拒绝归档。
- Knowledge 引用未知或不属于当前 Attempt 的 Observation 时拒绝。
- 两个不同 Executor Session 的来源才能产生 `corroborated`。
- review 不正确绑定冲突对时不改变任何 Knowledge 信任。
- 相同 Task 跨 Run 可以使用 stable/instance 知识；不同 Task 永不共享。

### 14.3 生命周期与安全

- Agent 自述成功、`local_success`、Observation 和 Knowledge 全部不能产生成功 verdict。
- backend completion 到达时取消在飞 Attempt，最终只使用 backend 权威结果。
- Store 或 ArtifactStore 任一写入失败会停止生产者并产生 failed Run。
- 取消后没有事件在 channel 关闭后写入，`go test -race ./...` 通过。
- 已知 secret 不出现在任何 SQLite 表、Artifact、日志、错误或 verdict 中。
- Docker 配置测试断言无 socket、只读根、no-new-privileges、capability drop 和资源限制。
- 多 Run 共库时 Branch/Attempt/Observation ID 不冲突。
- 迁移保留旧 events/logs，重复打开幂等。

### 14.4 文档与适配器

- README 不再声称 SDK 强制目标网络隔离；明确 `caller_managed`。
- 根包不导入 `pi`、Docker、TSec、SQLite、server 或 CLI。
- 仓库中不存在 `Workflow` 双控制路径或不可达 Knowledge trust 状态。
- TSec、CLI、HTTP、Web 都有从 `RunDescriptor` 到新引擎的明确映射。
- `go test ./...`、`go test -race ./...`、`go vet ./...`、`go build ./cmd/harness` 和前端 lint/typecheck/test/build 通过。

## 15. 实现完成定义

v1 完成必须同时满足：

- 设计的每个 C1-C12 约束都有唯一强制点、具名测试和失败模式。
- 根包公开 API 可以仅依赖标准库编译，并由外部 adapter composition 启动一个 Run。
- Pi 与 Docker 默认组合可以完成 planner → executor → observation → knowledge → verifier 的端到端流程。
- TSec 仍可启动 challenge 并通过 backend completion/verifier 形成权威结果。
- CLI、HTTP、SSE 和 Web 不绕开引擎建立第二控制路径。
- 所有迁移、失败、取消、清理、secret 与 race 测试通过。
