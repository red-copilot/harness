# Spike: Pi `new_session` 是否清空上下文

- 日期：2026-10-21
- 目的：确认"每轮全新上下文"可以用 Pi RPC 的 `new_session` 实现，且 `/workspace/report.json`
  可以从宿主侧读到。
- 相关设计：`docs/superpowers/specs/2026-10-21-exploration-expansion-design.md` §7、§11（S1）

## 环境

| 项 | 值 |
|---|---|
| Pi 版本 | **1.0.4**（`harness/pi-kali:local` 镜像内 `pi --version`） |
| 镜像 | `harness/pi-kali:local` |
| provider / model | `opencode-go` / `minimax-m3.1-flash-free`（来自 `.env`，非密钥） |
| 密钥 | 仅通过 `OPENCODE_API_KEY` 环境变量传入，**未写入本文档** |

备注：`ghcr.io/red-copilot/harness-pi-kali:0.1.0` 在本机不存在且 registry 拒绝拉取，因此使用本地构建的
`harness/pi-kali:local`（同版本 Pi，RPC 行为一致）。

## 尝试 1：真实模型 RPC 交互（未完成）

脚本启动 `pi --mode rpc`，依次发送：

```json
{"id":"1","type":"prompt","message":"Reply with the single word MEMORY."}
{"id":"2","type":"get_state"}
{"id":"3","type":"new_session"}
{"id":"4","type":"get_state"}
{"id":"5","type":"prompt","message":"...If you have no earlier instruction, reply exactly: NO_CONTEXT. Also write the single line ok into /workspace/report.json"}
```

结果：Pi 进程正常启动，但第一条 prompt 在约 3 分钟内没有等到 `agent_settled`（免费模型无响应），
交互被中止，容器与脚本已清理。**这不能证明 `new_session` 失败，只说明免费模型在当时不可用。**

## 尝试 2：Pi 1.0.4 源码级验证（完成）

在 Pi 包的 `dist/` 中找到实际实现：

1. `dist/modes/rpc/rpc-mode.js:334-340` —— RPC 命令 `new_session`：

   ```js
   case "new_session": {
       const options = command.parentSession ? { parentSession: command.parentSession } : undefined;
       const result = await runtimeHost.newSession(options);
       if (!result.cancelled) {
           await rebindSession();
       }
       return success(id, "new_session", result);
   }
   ```

2. `dist/core/agent-session-runtime.js:147-174` —— `newSession()` 的实现：

   ```js
   async newSession(options) {
       ...
       const sessionManager = this.session.sessionManager.isPersisted()
           ? SessionManager.create(this.cwd, sessionDir)
           : SessionManager.inMemory(this.cwd);
       ...
       await this.teardownCurrent("new", sessionManager.getSessionFile());
       this.apply(await this.createRuntime({
           cwd: this.cwd,
           agentDir: this.services.agentDir,
           sessionManager,
           sessionStartEvent: { type: "session_start", reason: "new", previousSessionFile },
       }));
       ...
   }
   ```

   即：**销毁当前 session runtime，新建一个全新的 `SessionManager`（`--no-session` 下为 in-memory），
   再创建一个新的 runtime**。消息列表属于 `session.messages`，新 runtime 从空开始。

3. `get_state` 返回的 `messageCount` 直接读 `session.messages.length`（同文件 `rpc-mode.js` 的 State 分支），
   因此 `new_session` 之后 `messageCount == 0`。

## 结论

- **`new_session` 会清空上下文**：它替换整个 session runtime，而不是只换 session id。
  设计中的 `ResettableSession{ Reset(ctx) error }` 可以用 `{"type":"new_session"}` 实现。
- `--no-session` 模式下 `SessionManager.inMemory(cwd)` 仍是全新对象，行为一致。
- **残余风险**：模型层的端到端验证（新会话确实"不记得"旧内容）推迟到 Task 9 的真实 TSec 跑通；
  每轮 Controller 都会调用 `Reset()`，若语义不符会在轮次报告中暴露。届时把实测结果补记到验证文档。
- `report.json` 的落盘路径不依赖模型：Explorer 用 `write` 工具写入 `/workspace/report.json`，
  该目录是宿主侧可读的 bind mount（与现有 session workspace 机制一致）。

## 复用命令

```bash
docker run --rm -i --name pi-spike \
  -v "$PWD/spike-ws:/workspace" -w /workspace \
  -e OPENCODE_API_KEY -e PI_PROVIDER -e PI_MODEL \
  harness/pi-kali:local \
  pi --mode rpc --no-session --no-approve --no-context-files --no-extensions --no-mcp \
     --no-prompt-templates --no-themes --no-skills --tools read,bash,write --offline \
     --provider "$PI_PROVIDER" --model "$PI_MODEL"
```

（不要在日志、文档或提交里写入密钥值。）
