# v5 端口契约

> **状态：v5 目标设计（提案）。** 当前代码仍是 v4 / schema 8。

## 1. 原则

- 端口由消费者（`engine`）定义，适配器实现。
- 一个端口只表达一个关注点；禁止把 Run CRUD、board 读取、artifact、恢复、进程生命周期塞进同一个并集接口。
- 写端口只接受已密封（redacted）的类型；无法在签名上绕过脱敏。
- 端口方法不 panic；错误带上下文并可用 `errors.Is` 判别。
- 端口不暴露 SQL、Docker、进程、环境变量等适配器细节。

## 2. 端口定义

```go
package engine

type Clock interface {
    Now() time.Time
}

type IDSource interface {
    NewRunID() (string, error)
    NewEntityID(runID, kind string) (string, error) // 不透明；禁止解析序号
}

type RunDirs interface {
    TaskDir() string
    SkillsDir() string
    SessionDir(sessionID string) string
}

type Workspace interface {
    PrepareRun(ctx context.Context, runID string) (RunDirs, error)
    VerifierDir(ctx context.Context, runID string) (dir string, cleanup func() error, err error)
}

// ---- 持久化 ----

type AppendResult struct {
    State   core.State
    Records []core.Record
}

type Journal interface {
    Create(ctx context.Context, m core.SealedManifest, first core.SealedBatch) (core.State, error)
    Append(ctx context.Context, b core.SealedBatch) (AppendResult, error)
    Finalize(ctx context.Context, f core.SealedFinalization) (AppendResult, error)
}

type Reader interface {
    Manifest(ctx context.Context, runID string) (core.Manifest, error)
    State(ctx context.Context, runID string) (core.State, error)
    Events(ctx context.Context, runID string, after uint64) ([]core.Record, error)
    Logs(ctx context.Context, runID string, after uint64) ([]core.LogRecord, error)
    Verdict(ctx context.Context, runID string) (*core.Verdict, error)
    Board(ctx context.Context, runID string) (core.Board, error)
    ListArtifacts(ctx context.Context, runID string) ([]core.Artifact, error)
    OpenArtifact(ctx context.Context, sha256 string) (io.ReadCloser, error)
    VerifyChain(ctx context.Context, runID string) error
}

type Recovery interface {
    NonTerminal(ctx context.Context) ([]core.State, error)
    Replay(ctx context.Context, runID string) (core.Restored, error)
    Interrupt(ctx context.Context, runID string, expectedHead uint64, reason string) (bool, error)
}

type ArtifactWriter interface {
    Put(ctx context.Context, a core.SealedArtifact) (core.Artifact, error)
}

type LogWriter interface {
    AppendLogs(ctx context.Context, runID string, logs []core.SealedLog) error
}

// ---- 执行 ----

type SessionRequest struct {
    RunID     string
    Role      core.AgentRole
    Workspace RunDirs
    Config    core.AgentDescriptor
}

type Session interface {
    Prompt(ctx context.Context, prompt string) error
    Reset(ctx context.Context) error
    Events() <-chan core.AgentEvent
    Logs() <-chan core.LogRecord
    Usage() core.Usage
    Abort(ctx context.Context) error
    Close(ctx context.Context) error
}

type SessionFactory interface {
    NewSession(ctx context.Context, req SessionRequest) (Session, error)
}

type TargetProvisioner interface {
    Provision(ctx context.Context, req core.ProvisionRequest) (core.TargetBinding, error)
    Close(ctx context.Context, binding core.TargetBinding) error
}

type Verifier interface {
    Verify(ctx context.Context, req core.VerifyRequest) (core.Verdict, error)
}

// ---- 安全 ----

type SecretSource interface {
    RedactorFor(runID string) (*core.Redactor, error)
}
```

`core.Finalization` 由 kernel 构造，至少包含：

```go
type Finalization struct {
    RunID        string
    ExpectedHead uint64
    Outcome      Outcome
    Error        string
    Usage        Usage
    Verdict      *Verdict
    Records      []Record
    FinishedAt   time.Time
}
```

`Journal.Finalize` 只接受 `core.SealedFinalization`；它由 `(*core.Redactor).SealFinalization` 从 `Finalization` 构造，确保 `Error`、`Verdict.Message/Evidence` 与 records payload 在落盘前已脱敏。

## 3. 归属

| 端口 | 消费方 | 生产实现 | 说明 |
|---|---|---|---|
| `Clock` | engine | `runtime` 提供 `RealClock`；测试 fake | 禁止直接 `time.Now` |
| `IDSource` | engine | `runtime` 提供 crypto/rand 实现；测试 fake | 不透明 ID |
| `Workspace` | engine | `runtime` 提供文件系统实现；测试临时目录 | 唯一目录创建入口 |
| `Journal` | engine | `store/sqlite`；P4 的临时 v4 桥 | 唯一规范写入口 |
| `Reader` | engine、CLI | `store/sqlite` | 只读查询 |
| `Recovery` | engine、CLI | `store/sqlite` | 回放、非终态列举、中断 |
| `ArtifactWriter` | engine | `store/sqlite` | 只接受 `SealedArtifact` |
| `LogWriter` | engine | `store/sqlite` | 只接受 `SealedLog` |
| `SessionFactory` | engine | `pi` | 每个 Run 两个 session |
| `TargetProvisioner` | engine | `sandbox/docker` | 目标供应/清理 |
| `Verifier` | engine | `verify` 或调用方实现 | 成功唯一来源 |
| `SecretSource` | engine | `runtime` | 从配置/环境收集已知 secret，构造 `Redactor` |

`engine.Config`：

```go
type Config struct {
    Journal     Journal
    Reader      Reader
    Recovery    Recovery
    Artifacts   ArtifactWriter
    Logs        LogWriter
    Sessions    SessionFactory
    Targets     TargetProvisioner
    Verifier    Verifier
    Secrets     SecretSource
    Workspace   Workspace
    Clock       Clock
    IDs         IDSource
    MaxSessions int
    Timeouts    Timeouts
}
```

`Verifier`、`Journal`、`Reader`、`Recovery`、`Sessions`、`Targets`、`Workspace`、`Clock`、`IDs` 为必填；缺失在 `New` 时 fail closed。

## 4. 被删除的端口

| v4 | 处理 |
|---|---|
| `Store`（21 方法并集） | 拆为 `Journal`/`Reader`/`Recovery`/`ArtifactWriter`/`LogWriter` |
| `CompletionSource`、`Backend` | 删除；仅 verifier 权威 |
| `CanonicalWriter`、`ReadStore`、`BoardReader`、`BoardRestorer`、`RunRecovery` | 删除；由上述窄端口替代 |
| `ArtifactStore` | 拆为 `ArtifactWriter` + `Reader.OpenArtifact` |
| `EventSink` | 删除；事件由 controller 的 journal 写入路径产生 |
| `ProcessStarter`、`ResourceCleaner` | 从 kernel 移除；由 `pi`/`sandbox/docker` 与 `runtime` 负责 |
| `AgentRuntimeFactory`/`AgentSession` | 由 `SessionFactory`/`Session` 替代，使用 `core` 类型 |

## 5. 错误语义

| 错误 | 含义 | 调用方动作 |
|---|---|---|
| `ErrStaleHead` | `ExpectedHead` 与 `run_state.head_seq` 不匹配 | 重新读取 state；不要盲目重试 |
| `ErrTerminal` | Run 已 terminal | 拒绝写入；resume 也拒绝 |
| `ErrNotFound` | Run/manifest/record 不存在 | 报错，不创建 |
| `ErrCorruptJournal` | 链/回放/checkpoint 校验失败 | fail closed；操作者中断 |
| `ErrUnauthorizedTarget` | 证据引用了未授权目标 | 拒绝证据/Claim |
| `ErrEvidencePath` | 路径逃逸/符号链接/大小超限 | 拒绝证据 |
| `ErrBudgetExceeded` | 机械预算超限 | 终止为非成功 |
| `ErrRunClosed` | Runner 已关闭，拒绝新写入 | 正常关闭路径 |
| `ErrLeaseHeld` | 另一个进程持有写租约 | 拒绝启动写者 |

所有端口错误必须包装底层原因：`fmt.Errorf("append run %s: %w", runID, err)`。

## 6. 端口一致性测试（`engine/porttest`）

每个实现必须通过同一套测试：

1. `Create` 写入 manifest、首条记录、state；重复 run id 失败。
2. `Append` 校验 head；stale head 返回 `ErrStaleHead` 且无部分写入。
3. 链：篡改任一记录字节/prev/hash 后 `VerifyChain` 失败。
4. terminal 后 append 返回 `ErrTerminal`；触发器同样拒绝。
5. `Finalize`：成功且无 verdict 失败；`succeeded` + `success=false` 失败；非成功 + verdict 成功失败。
6. finalize 原子性：注入任一步失败，不出现 terminal 无 finalization 或 succeeded 无 verdict。
7. checkpoint：在任意 seq 写入后可从 checkpoint+tail 回放；损坏 checkpoint 回退全量。
8. 读模型：删除 `rm_*` 后重建，与 `Reader.Board` 一致。
9. artifact：同内容同 ID；哈希/size 不符拒绝；`SealedArtifact` 之外无法写入。
10. logs：seq 单调、terminal 后拒绝、内容已密封。
11. `Recovery.Interrupt`：expected head 不匹配失败；terminal 失败；成功只发生一次。
12. 并发：同一 Run 两个 append 只有一个成功；resume 与 interrupt 只有一个成功。

v5 store 与 P4 的 v4 兼容桥都必须通过同一套 `porttest`；桥的差异用 adapter-specific 测试补充。

## 7. 端口演进

- 增加方法 = 破坏所有实现；优先新增一个窄端口并在组合根注入。
- 禁止可选方法/类型断言；端口要么实现，要么不满足。
- 端口签名中的类型必须来自 `core` 或标准库；禁止适配器类型。
- 任何新增写端口都必须接受 `Sealed*` 类型，否则 arch 测试失败。
