# 探索扩散架构（单 Controller + 两个短期 Explorer）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用"1 个只读 Controller + 2 个短命 Explorer + 共享事实账本"替换现有单 Agent 模板化 Frontier 探索路径，实现每轮恰好 2 条探索项的并行扩散、事实级信任升级与冲突复核裁决。

**Architecture:** Controller 是唯一顺序执行者，每轮：规划（LLM，只读无 bash）→ 并行分派 2 条 item 给 2 个全新上下文的 Explorer → 校验结构化报告并归并进事实账本 → 应用冲突/裁决 → 下一轮。所有状态以事件落 SQLite，事实账本在进程内维护。所有跨轮记忆在账本里，不在任何会话上下文里。

**Tech Stack:** Go 1.x（标准库为主）、Pi RPC（LF JSONL / `new_session` / `agent_settled`）、SQLite（`store/sqlite`）、React 19 + Vite + Vitest（`web/`）。

**Spec:** `docs/superpowers/specs/2026-10-21-exploration-expansion-design.md`

---

## Global Constraints

以下值直接来自 spec，**逐字执行，不得改动**：

- 成功只能由 `Verifier` 或 backend completion 判定。Controller 输出、Explorer 报告、事实账本中的任何内容都**不得**产生成功判定。
- 每轮分派 item 数：**恰好 2**（不可配置）；不足时按 spec §7.1 允许 1 条单条轮次，0 条则结束。
- `MaxRounds` 默认 **6**。
- 事实账本上限：**200** 条事实、**100** 条 item、每目标 **≤40** 条事实。
- 文本上限：fact claim **300B**、fact evidence **400B**、注入摘要中的 claim **200B**。
- 注入摘要（digest）最多 **8** 条事实；`refuted` 事实**不注入**，`contested` 事实注入但必须标注。
- Explorer 报告中的 `next` 提案最多保留 **3** 条。
- 每轮每 Explorer 工具调用上限 **40**，超限即 Abort。
- Explorer 报告路径：**`/workspace/report.json`**（宿主侧读 `<sessionWorkspace>/report.json`）。
- Controller 输出为 **JSON**，字段：`next_items` / `contradictions` / `resolutions`；`next_items` 必须为 **2** 条。
- Controller 权限：**只读**，Pi 工具集为 `["read"]`（无 bash、无写）。
- 授权边界：item 的 target 必须匹配 `Task.Targets` 的 `ID`/`Addresses` 或其派生的 host/port/path；越界 item **丢弃 + 记诊断**，不影响同轮另一条。
- anchor 规则：只能引用账本中 `trust == corroborated` 的事实；**唯一例外**是 `ReviewOf` 非空的冲突复核 item（可 anchor `contested` 事实）。
- 冲突策略 A：双方都保留、都标 `contested`、下一轮生成**一条**复核 item、同一对事实**最多复核一次**、复核后由 Controller 裁决（保留方 → `corroborated`，对方 → `refuted`）。**绝不静默覆盖。**
- `corroborated` 永远意味着 **≥2 个不同 session**；`refuted` 永远来自一次显式裁决。
- Explorer 可直接调用 `tsec-submit` 提交 flag（spec D15）；harness 不解析报告里的 flag。
- Pi RPC：严格 LF JSONL；`agent_settled` 才是完成，`agent_end` 不是。
- 容器：无 Docker socket、只读根、`no-new-privileges`、drop caps、资源限额；容器名由 run 身份派生。
- 写入前脱敏；`BENCHMARK_TOKEN` 等不得明文落 SQLite。
- 先写测试再实现；负面/失败关闭用例必须覆盖：报告缺失/非法 JSON/`item_id` 不匹配/`verdict` 非法/悬空 `supports`/`refutes`、Controller 输出坏 JSON、planner 降级、预算耗尽、session 中止、报告截断。
- 提交信息用 Conventional Commits 且带 scope；每个任务验证通过后单独 commit。

---

## Review Focus

以下输入/故障模式 spec 隐含但没有任务直接覆盖，最容易在真实使用中咬人。每条都已在对应任务的步骤里落成测试：

1. **Controller 返回坏 JSON 或不是恰好 2 条 item** → 本轮降级为确定性补齐/种子 item，run **继续**且不产生成功判定（Task 5）。
2. **Explorer 报告的 `item_id` 与被分配 item 不符** → 该 item 判 `failed`、**不入事实库**，同轮另一条 item 不受影响（Task 3）。
3. **两个 Explorer 用不同措辞断言同一件事** → 通过显式 `supports` 引用归并为一条事实并升为 `corroborated`，不产生重复事实（Task 2）。
4. **两条断言互相矛盾** → 双方都保留、都标 `contested`、只生成**一条**复核 item、复核后裁决，不静默覆盖（Task 2 + Task 5）。
5. **轮次中途预算/墙钟耗尽** → run 终止并给出"无权威判定"的失败结论，**不得**产出成功 verdict（Task 7）。

---

## 文件结构

| 文件 | 职责 |
|---|---|
| `types.go`（改） | 新公开类型：`FactTrust`、`Fact`、`FrontierStatus`、`FrontierItem`、`ExplorerClaim`、`ExplorerProposal`、`ExplorerReport`、`Contradiction`、`Resolution`、`ExplorationSpec`、`ResettableSession`；删除旧的 `FrontierKind`/`Frontier`/`ExplorationContext` 等 |
| `facts.go`（新） | 事实账本：合并、信任升级、冲突标记、上限裁剪 |
| `explorer.go`（新） | 探索者报告的解析与校验、Explorer 简报构造、目标授权校验 |
| `planner.go`（新） | Controller 规划：请求/输出类型、prompt 构造、输出解析与校验、降级种子 |
| `exploration_controller.go`（新） | 轮循环：选两条、并行执行、归并、裁决、终止判定、事件发射 |
| `session_agent.go`（新） | 把 `AgentSession` 适配成 `Explorer` 与 `Planner`（跑一轮 turn、读 report.json、抽取 assistant 文本） |
| `pi/session.go`（改） | 实现 `Reset()`：发送 `{"type":"new_session"}` |
| `runner.go`（改） | 用 Controller 轮循环替换"prompt 一次 + steer"；新事件发射；每轮后调用 Verifier |
| `state.go`（删） | 旧模板化状态投影，被 `facts.go` 取代 |
| `frontier.go`（删） | 旧模板化 Frontier，被 `explorer.go`/`planner.go` 取代 |
| `exploration_test.go`（重写） | 新控制环的端到端测试 |
| `web/src/utils/exploration.ts`（改） | 事件 → 轮次视图模型 |
| `web/src/components/ExplorationTimeline.tsx`（改） | 轮次视图 UI |
| `web/src/pages/RunDetailPage.tsx`（改） | 接入新组件 |
| `docs/architecture.md`（改） | 更新探索控制章节 |

---

### Task 1: Spike —— 确认 Pi `new_session` 清空上下文，且 `report.json` 可从宿主侧读到

**Files:**
- Create: `docs/superpowers/spikes/2026-10-21-pi-new-session.md`（记录实测证据）

**Interfaces:**
- Consumes: 现有 `pi.BuildCommand` 生成的 Pi 启动参数；仓库根 `.env` 中的 `OPENCODE_API_KEY` / `PI_PROVIDER` / `PI_MODEL`（**只使用变量名，不要把值写进文档**）。
- Produces: 一份 spike 结论，供 Task 6（`Reset()` 实现）与 Task 3（报告落盘路径）依赖。

- [ ] **Step 1: 记录基线**

在仓库根执行：

```bash
docker run --rm -i -v "$PWD/spike-ws:/workspace" -w /workspace \
  ghcr.io/red-copilot/harness-pi-kali:0.1.0 \
  pi --mode rpc --no-session --no-approve --no-context-files --no-extensions --no-mcp \
     --no-prompt-templates --no-themes --no-skills --tools read,bash,write --offline \
     --provider "$PI_PROVIDER" --model "$PI_MODEL"
```

（`PI_PROVIDER` / `PI_MODEL` / `OPENCODE_API_KEY` 从 `.env` 读入环境，不要打印其值。）

- [ ] **Step 2: 验证 `new_session` 清空消息**

依次向该 stdin 发送（每行一个 JSON 对象，LF 结尾）：

```json
{"id":"1","type":"prompt","message":"Reply with the single word MEMORY."}
{"id":"2","type":"get_state"}
{"id":"3","type":"new_session"}
{"id":"4","type":"get_state"}
{"id":"5","type":"prompt","message":"What single word were you told to reply with earlier in this conversation? If you have no earlier instruction, reply exactly: NO_CONTEXT."}
```

期望：响应 `2` 的 `messageCount > 0`；响应 `4` 的 `messageCount == 0`；响应 `5` 的最终文本是 `NO_CONTEXT`。

- [ ] **Step 3: 验证 `report.json` 从宿主侧可读**

在第 5 步的 prompt 里追加要求 "Write the single line `ok` into /workspace/report.json"，结束后在宿主侧执行 `cat spike-ws/report.json`，期望输出 `ok`。

- [ ] **Step 4: 把结论写进 spike 文档**

`docs/superpowers/spikes/2026-10-21-pi-new-session.md` 必须包含：三段命令、`messageCount` 实测值、响应 5 的实际文本、`report.json` 的实际内容、以及一句话结论（`new_session` 是否清空上下文）。**不得写入任何密钥、token 或 flag**。

- [ ] **Step 5: 清理并提交**

```bash
rm -rf spike-ws
git add docs/superpowers/spikes/2026-10-21-pi-new-session.md
git commit -m "docs(spike): verify pi new_session resets context"
```

---

### Task 2: 事实账本（`facts.go`）

**Files:**
- Create: `facts.go`
- Create: `facts_test.go`
- Modify: `types.go`（加入 `FactTrust`、`Fact` 类型）

**Interfaces:**
- Consumes: 无（纯逻辑任务）。
- Produces:
  ```go
  // types.go
  type FactTrust string
  const (
      FactObserved     FactTrust = "observed"
      FactCorroborated FactTrust = "corroborated"
      FactRefuted      FactTrust = "refuted"
  )
  type Fact struct {
      ID         string    `json:"id"`
      Claim      string    `json:"claim"`
      Evidence   string    `json:"evidence"`
      Source     string    `json:"source"`
      Sources    []string  `json:"sources"`
      Trust      FactTrust `json:"trust"`
      Contested  bool      `json:"contested"`
      Targets    []string  `json:"targets,omitempty"`
      RefutedBy  []string  `json:"refuted_by,omitempty"`
  }

  // facts.go
  const (
      MaxFacts         = 200
      MaxItems         = 100
      MaxFactsPerTarget = 40
      MaxClaimBytes    = 300
      MaxEvidenceBytes = 400
  )

  type FactLedger struct { /* ... */ }
  func NewFactLedger() *FactLedger
  func (l *FactLedger) Facts() []Fact
  func (l *FactLedger) Get(id string) (Fact, bool)
  func (l *FactLedger) Corroborated() []Fact
  func (l *FactLedger) MergeClaims(round int, itemID, sessionID string, claims []ExplorerClaim) MergeResult
  func (l *FactLedger) MarkRefuted(factID, byFactID string)

  type MergeResult struct {
      Added        []string   // 新建事实的 ID
      Corroborated []string   // 本次升级为 corroborated 的事实 ID
      Contested    [][2]string // 本次标记为 contested 的事实对
      Updated      []string   // trust 发生变化的事实 ID
  }
  ```

  `ExplorerClaim`（定义在 `types.go`，本任务只用到它）：
  ```go
  type ExplorerClaim struct {
      Claim    string   `json:"claim"`
      Evidence string   `json:"evidence"`
      Targets  []string `json:"targets,omitempty"`
      Supports []string `json:"supports,omitempty"`
      Refutes  []string `json:"refutes,omitempty"`
  }
  ```

- [ ] **Step 1: 写失败测试** `facts_test.go`，覆盖：

  - `TestFactLedgerMergeAddsObservedFact`：一条无引用 claim → `Added` 含 1 个 ID，新事实 `Trust == FactObserved`，`Sources == [sessionID]`。
  - `TestFactLedgerMergeCorroboratesOnExplicitSupport`：session A 建事实 `f-1`；session B 的 claim 带 `Supports: ["f-1"]` 且 claim 文本不同 → `f-1` 的 `Sources` 含两个不同 session，`Trust == FactCorroborated`，且**不新增**事实（`Added` 为空）。
  - `TestFactLedgerMergeCorroboratesIdenticalClaimText`：两个 session 断言完全相同的归一化 claim（无 `supports`）→ 归并为同一条事实并升为 `corroborated`。
  - `TestFactLedgerRefutationMarksBothContestedWithoutSilentOverwrite`：session A 建 `f-1`（corroborated）；session B 的 claim `Refutes: ["f-1"]` → `f-1` **仍然存在**、`Contested == true`；新事实存在、`Contested == true`、`Trust == FactObserved`；`MergeResult.Contested` 长度为 1。
  - `TestFactLedgerRefutationDoesNotPromoteToRefutedImmediately`：同上，断言没有任何事实 `Trust == FactRefuted`。
  - `TestFactLedgerTruncatesClaimAndEvidence`：超长 claim/evidence 被截断到 `MaxClaimBytes` / `MaxEvidenceBytes`。
  - `TestFactLedgerDropsDanglingReferences`：`Supports`/`Refutes` 指向不存在 ID → 引用被丢弃，claim 仍作为新事实入库（`observed`）。
  - `TestFactLedgerEnforcesLimits`：入库 250 条事实 → `len(l.Facts()) <= MaxFacts`；同一 target 超过 `MaxFactsPerTarget` 条 → 该 target 的条数不超限。
  - `TestFactLedgerRefutedOnlyByExplicitResolution`：`MergeResult` 之后调用方把某事实置 `refuted` 的唯一入口是 `MarkRefuted(factID, byFactID)`；测试断言 `MergeClaims` 不会产生 `refuted`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./... -run TestFactLedger`
Expected: FAIL（`undefined: NewFactLedger` 等）

- [ ] **Step 3: 实现**

在 `types.go` 加 `FactTrust`、`Fact`、`ExplorerClaim`；新建 `facts.go`：

- `MergeClaims` 的判定顺序（每条 claim 依次）：
  1. `Claim` 或 `Evidence` 为空 → 跳过该条。
  2. 截断 claim 到 `MaxClaimBytes`、evidence 到 `MaxEvidenceBytes`。
  3. `Supports` 中存在的 ID → 把 `sessionID` 加入该事实的 `Sources`（去重）；`len(Sources) >= 2` → `Trust = FactCorroborated`，记入 `Corroborated`；该 claim **不再**新建事实。
  4. `Refutes` 中存在的 ID → 该事实 `Contested = true`；本条 claim 若尚无归属事实，则新建一条 `Trust = FactObserved`、`Contested = true` 的事实，并把这一对记入 `Contested`。
  5. 既无 supports 又无 refutes 命中 → 归一化 claim 文本（去空白、转小写、去标点）后与同 target 的既有事实比较；完全相同 → 视作同一事实的新来源（走第 3 步的升级逻辑）；否则新建 `observed` 事实。
- 事实 ID 形如 `f-<round>-<seq>`，由调用方传入的 `itemID` 无法生成 ID → 改为 `MergeClaims(round int, itemID, sessionID string, claims []ExplorerClaim) MergeResult`，ID 生成规则 `f-<round>-<seq>`（`seq` 在账本内自增）。
- 上限裁剪：超过 `MaxFacts` 时，优先保留 `corroborated` 与最新事实；同一 target 超过 `MaxFactsPerTarget` 时丢弃该 target 最老的 `observed` 事实。
- 额外提供 `func (l *FactLedger) MarkRefuted(factID, byFactID string)`，唯一产生 `FactRefuted` 的入口。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./... -run TestFactLedger`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add facts.go facts_test.go types.go
git commit -m "feat(facts): add fact ledger with trust transitions"
```

---

### Task 3: 探索者报告解析、简报构造与授权目标校验（`explorer.go`）

**Files:**
- Create: `explorer.go`
- Create: `explorer_test.go`
- Modify: `types.go`（加入 `FrontierStatus`、`FrontierItem`、`ExplorerProposal`、`ExplorerReport`）

**Interfaces:**
- Consumes: Task 2 的 `Fact` / `FactLedger`（`FactLedger.Get`、`FactLedger.Corroborated`）。
- Produces:
  ```go
  // types.go
  type FrontierStatus string
  const (
      FrontierOpen     FrontierStatus = "open"
      FrontierAssigned FrontierStatus = "assigned"
      FrontierDone     FrontierStatus = "done"
      FrontierFailed   FrontierStatus = "failed"
  )
  type FrontierItem struct {
      ID               string         `json:"id"`
      ParentID         string         `json:"parent_id,omitempty"`
      Question         string         `json:"question"`
      TargetHint       string         `json:"target_hint"`
      ExpectedEvidence string         `json:"expected_evidence"`
      Anchors          []string       `json:"anchors,omitempty"`
      ReviewOf         []string       `json:"review_of,omitempty"`
      Status           FrontierStatus `json:"status"`
      AssignedTo       string         `json:"assigned_to,omitempty"`
      Round            int            `json:"round"`
  }
  type ExplorerProposal struct {
      Question         string   `json:"question"`
      Target           string   `json:"target"`
      ExpectedEvidence string   `json:"expected_evidence"`
      Anchors          []string `json:"anchors,omitempty"`
  }
  type ExplorerReport struct {
      ItemID   string            `json:"item_id"`
      Verdict  string            `json:"verdict"`
      Claims   []ExplorerClaim   `json:"claims"`
      Next     []ExplorerProposal `json:"next"`
      Commands []string          `json:"commands"`
      Errors   []string          `json:"errors"`
  }

  // explorer.go
  const (
      MaxDigestFacts = 8
      MaxDigestClaimBytes = 200
      MaxProposals    = 3
      ReportFileName = "report.json"
  )
  const (
      VerdictFound        = "found"
      VerdictNotFound     = "not_found"
      VerdictInconclusive = "inconclusive"
  )

  func ParseExplorerReport(data []byte, item FrontierItem) (ExplorerReport, error)
  func BuildExplorerBrief(objective string, targets []Target, item FrontierItem, facts []Fact) string
  func DigestFacts(item FrontierItem, facts []Fact) []Fact
  func AuthorizeTarget(target string, targets []Target) bool
  ```

- [ ] **Step 1: 写失败测试** `explorer_test.go`，覆盖：

  - `TestParseExplorerReportRejectsMismatchedItemID`：`item_id` 与被分配 item 不同 → 返回 error。
  - `TestParseExplorerReportRejectsInvalidVerdict`：`verdict` 为 `"maybe"` → error。
  - `TestParseExplorerReportRejectsMalformedJSON`：`not json` → error。
  - `TestParseExplorerReportDropsClaimsWithoutEvidence`：一条 claim 缺 `evidence` 被丢弃，另一条完整 claim 保留。
  - `TestParseExplorerReportCapsProposalsAtThree`：`next` 有 5 条 → 只保留 3 条。
  - `TestBuildExplorerBriefContainsExactlyOneItem`：简报文本里只出现被分配 item 的 question，不出现其他 item 的 question；且包含 "Objective"、question、"report.json"、"corroborated"。
  - `TestDigestFactsIsBoundedAndExcludesRefuted`：`DigestFacts` 返回 ≤ `MaxDigestFacts` 条；`refuted` 事实不在结果里；`contested` 事实在结果里且带标注。
  - `TestDigestFactsOnlyIncludesRelatedFacts`：只有 `Targets` 命中或 `Anchors` 命中的事实入选。
  - `TestAuthorizeTargetAcceptsDerivedHostPort`：`Target{ID:"web", Addresses:["10.0.0.5:80"]}` 授权 `10.0.0.5`、`10.0.0.5:8080`、`10.0.0.5/admin`；不授权 `10.0.0.6`、`example.com`。
  - `TestAuthorizeTargetRejectsUnrelatedHost`：目标集合外的地址 → `false`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./... -run "TestParseExplorerReport|TestBuildExplorerBrief|TestDigestFacts|TestAuthorizeTarget"`
Expected: FAIL（undefined symbols）

- [ ] **Step 3: 实现**

`ParseExplorerReport`：
- `json.Unmarshal` 失败 → error（消息含 `parse explorer report`）。
- `ItemID != item.ID` → error（含 `item_id`）。
- `Verdict` 不在三个常量内 → error（含 `verdict`）。
- `Claims`：逐条丢弃 `Claim` 或 `Evidence` 为空的条目；其余保留。
- `Next`：丢弃 `Question` 为空的条目，最多保留 `MaxProposals` 条。
- `Supports`/`Refutes` 中不存在的 ID 在 Task 2 归并阶段丢弃，此处原样保留。

`DigestFacts(item, facts)`：
- 只保留 `fact.Targets` 与 `item.TargetHint` 有交集、或 `item.Anchors` 含 `fact.ID` 的事实。
- 排除 `Trust == FactRefuted`。
- 按 `Trust` 降序（corroborated → observed）、`ID` 升序排序后取前 `MaxDigestFacts` 条。
- 返回的 `Fact.Claim` 截断到 `MaxDigestClaimBytes`。

`BuildExplorerBrief(objective, targets, item, facts)`：
固定五段，顺序固定：`Task objective:` → `Authorized targets:` → `Your assigned item:` → `Known facts / ruled out` → `Rules:`。Rules 里必须包含字面量 `report.json`、`corroborated`、`only the benchmark backend or the harness verifier`（对应 spec §7.3 第 2 条，允许提交 flag 但不得声称成功）。`Known facts` 段每条以 `- [f-1 corroborated] claim` 形式，`contested` 事实写作 `- [f-2 contested] claim`。

`AuthorizeTarget(target, targets)`：
- `target` 为空 → `false`。
- 与任一 `Target.ID` 或 `Target.Addresses` 完全相等（大小写不敏感）→ `true`。
- 从地址中解析出 host（去掉端口与路径）后，`target` 的 host 与之相等 → `true`（端口/路径可派生）。
- 否则 `false`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./... -run "TestParseExplorerReport|TestBuildExplorerBrief|TestDigestFacts|TestAuthorizeTarget"`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add explorer.go explorer_test.go types.go
git commit -m "feat(explorer): add report validation and explorer brief"
```

---

### Task 4: Controller 规划器（`planner.go`）

**Files:**
- Create: `planner.go`
- Create: `planner_test.go`
- Modify: `types.go`（加入 `Contradiction`、`Resolution`、`ExplorationSpec`、`ResettableSession`）

**Interfaces:**
- Consumes: Task 2 的 `Fact`；Task 3 的 `FrontierItem`、`ExplorerReport`、`ExplorerProposal`、`AuthorizeTarget`。
- Produces:
  ```go
  // types.go
  type Contradiction struct {
      A    string `json:"a"`
      B    string `json:"b"`
      Note string `json:"note,omitempty"`
  }
  type Resolution struct {
      A    string `json:"a"`
      B    string `json:"b"`
      Keep string `json:"keep"`
      Note string `json:"note,omitempty"`
  }
  type ExplorationSpec struct {
      Rounds            int      `json:"rounds,omitempty"`
      ControllerAgentID string   `json:"controller_agent_id"`
      ExplorerAgentIDs  []string `json:"explorer_agent_ids"`
  }
  type ResettableSession interface {
      Reset(context.Context) error
  }

  // planner.go
  type PlanRequest struct {
      Round      int
      Objective  string
      Targets    []Target
      Facts      []Fact
      OpenItems  []FrontierItem
      RecentItems []FrontierItem
      Reports    []ExplorerReport
  }
  type PlanOutput struct {
      NextItems     []ExplorerProposal `json:"next_items"`
      Contradictions []Contradiction   `json:"contradictions"`
      Resolutions    []Resolution      `json:"resolutions"`
  }
  type Planner interface {
      Plan(ctx context.Context, req PlanRequest) (PlanOutput, error)
  }

  func ParsePlanOutput(data []byte) (PlanOutput, error)
  func ValidatePlan(out PlanOutput, ledger *FactLedger, items []FrontierItem, targets []Target) (PlanValidation, error)
  type PlanValidation struct {
      Accepted      []FrontierItem
      Dropped       []DroppedItem
      Contradictions []Contradiction
      Resolutions    []Resolution
  }
  type DroppedItem struct {
      Proposal  ExplorerProposal
      Reason    string
  }
  func SeedItems(targets []Target, objective string, round int) []FrontierItem
  ```

- [ ] **Step 1: 写失败测试** `planner_test.go`，覆盖：

  - `TestParsePlanOutputRejectsMalformedJSON`：`not json` → error。
  - `TestValidatePlanRequiresExactlyTwoItems`：只给 1 条 → 返回 error 或 `Accepted` 长度为 0（由实现选定的降级路径决定，测试断言"不抛成功"且 `Accepted` 不多于 2）。
  - `TestValidatePlanDropsItemWithUnauthorizedTarget`：`target` 不在 `Task.Targets` → 该 proposal 出现在 `Dropped`，`Reason` 含 `unauthorized`。
  - `TestValidatePlanDropsAnchorThatIsNotCorroborated`：anchor 指向 `observed` 事实 → 该 anchor 被丢弃但 item 保留（`Accepted` 含该 item，`Anchors` 为空）。
  - `TestValidatePlanDropsDuplicateQuestion`：`question` 与已有 open item 归一化后相同 → 出现在 `Dropped`，`Reason` 含 `duplicate`。
  - `TestValidatePlanDropsItemWithoutQuestion`：`question` 为空 → `Dropped`。
  - `TestValidatePlanKeepsContradictionsForKnownFacts`：`contradictions` 引用存在的两个事实 → 原样进入 `PlanValidation.Contradictions`；引用不存在的 ID → 被丢弃。
  - `TestValidatePlanDropsResolutionForNonContestedPair`：两个未 contested 的事实 → 该 resolution 被丢弃。
  - `TestSeedItemsCreatesOneItemPerTarget`：2 个授权目标 → 2 条 seed item，`TargetHint` 分别为两个目标，`Status == FrontierOpen`。
  - `TestSeedItemsWithNoTargetsReturnsEmpty`：无目标 → 返回空切片。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./... -run "TestParsePlanOutput|TestValidatePlan|TestSeedItems"`
Expected: FAIL（undefined symbols）

- [ ] **Step 3: 实现**

`ParsePlanOutput`：`json.Unmarshal` 失败 → error（含 `parse plan output`）；未知字段忽略；`next_items` 缺字段时留空。

`ValidatePlan(out, ledger, items, targets)`：
- 遍历 `out.NextItems`：`Question` 为空 → drop（`empty question`）；`Target` 未通过 `AuthorizeTarget` → drop（`unauthorized target`）；与 `items` 中任一 item 的 question 归一化后相同 → drop（`duplicate question`）；`Anchors` 只保留账本中 `Trust == FactCorroborated` 的 ID；通过校验的按顺序构造 `FrontierItem`（`Status = FrontierOpen`、`Round` 由调用方设置）。
- `Contradictions`：`A`/`B` 必须都存在于账本，否则丢弃该条。
- `Resolutions`：`A`/`B` 必须都在账本且都是 `Contested == true`，否则丢弃。
- `Accepted` 最多 2 条；超出截断。

`SeedItems(targets, objective, round)`：每个 `Target`（ID 优先，其次每个 Address）生成一条 item，question = `摸清目标 <target> 的攻击面、可达服务与可利用入口`，`ExpectedEvidence` = `一份列出开放端口/服务/入口的观察证据`，`Status = FrontierOpen`、`Round = round`；目标为空 → 返回空切片。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./... -run "TestParsePlanOutput|TestValidatePlan|TestSeedItems"`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add planner.go planner_test.go types.go
git commit -m "feat(planner): add controller plan parsing and validation"
```

---

### Task 5: 轮循环（`exploration_controller.go`）

**Files:**
- Create: `exploration_controller.go`
- Create: `exploration_controller_test.go`

**Interfaces:**
- Consumes: Task 2 `FactLedger` / `MergeResult`；Task 3 `ParseExplorerReport`、`BuildExplorerBrief`、`DigestFacts`；Task 4 `Planner` / `PlanRequest` / `ValidatePlan` / `SeedItems`。
- Produces:
  ```go
  // exploration_controller.go
  type Explorer interface {
      ID() string
      Reset(ctx context.Context) error
      Investigate(ctx context.Context, item FrontierItem, brief string) (ExplorerReport, error)
  }
  type ControllerEvent struct {
      Type string
      Data map[string]any
  }
  type ControllerConfig struct {
      Rounds    int
      Objective string
      Targets   []Target
  }
  type Controller struct { /* ... */ }
  func NewController(cfg ControllerConfig, planner Planner, explorers []Explorer, emit func(ControllerEvent)) *Controller
  func (c *Controller) Run(ctx context.Context) error
  func (c *Controller) Items() []FrontierItem
  func (c *Controller) Facts() []Fact
  ```

  事件类型（`ControllerEvent.Type`，由 runner 落库时映射为 `exploration.<type>`）：
  `round.started` / `items.planned` / `item.assigned` / `report.received` / `facts.merged` /
  `contested` / `resolved` / `round.completed` / `item.failed` / `planner.degraded`。

- [ ] **Step 1: 写失败测试** `exploration_controller_test.go`（用假 Explorer / 假 Planner，不依赖 Pi），覆盖：

  - `TestControllerRunsTwoItemsPerRoundInParallel`：两个假 Explorer 各自收到 1 条 item、brief 里只含自己的 question；本轮 2 条。
  - `TestControllerGivesEachRoundFreshContext`：假 Explorer 记录 `Reset` 调用次数，轮数 ≥2 时每轮都调用 `Reset`。
  - `TestControllerAssignsDistinctItemsToDistinctExplorers`：两个 Explorer 拿到的 `item.ID` 不同。
  - `TestControllerBootstrapUsesSeedItemsWhenPlannerIsBroken`：Planner 返回坏 JSON → Controller 使用 `SeedItems` 启动，并发出 `planner.degraded` 事件，run **继续**。
  - `TestControllerFailsItemOnMismatchedReportItemID`：Explorer 返回 `item_id` 不匹配的报告 → Controller 检出 `report.ItemID != item.ID` → 该 item 状态 `failed`，**事实账本为空**，另一条 item 仍被归并。
  - `TestControllerDoesNotClaimSuccessFromReports`：所有报告 `verdict: "found"` → Controller 不产生任何成功相关事件（事件类型集合中不含 `success`），`Run` 返回 nil。
  - `TestControllerMarksContradictionContestedAndSchedulesOneReview`：Planner 输出 `contradictions` → 两个事实 `Contested == true`，且下一轮**只**多出 1 条 `ReviewOf` 非空的复核 item。
  - `TestControllerSchedulesAtMostOneReviewPerPair`：连续两轮 Planner 对同一对事实输出 `contradictions` → 复核 item 只有 1 条。
  - `TestControllerAppliesResolutionAfterReview`：复核完成后 Planner 输出 `resolutions{keep:A}` → A `Trust == FactCorroborated`，B `Trust == FactRefuted`。
  - `TestControllerStopsWhenNoItemsRemain`：Planner 不产出新 item 且无 open item → `Run` 返回 nil，轮次数 ≤ `Rounds`。
  - `TestControllerStopsAfterMaxRounds`：`Rounds: 2` → 最多 2 轮。
  - `TestControllerRunsSingleItemRoundWhenOnlyOneItemAvailable`：只有 1 条 open item → 本轮 1 条，事件 `items.planned` 的 `batch_size == 1`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./... -run TestController`
Expected: FAIL（undefined symbols）

- [ ] **Step 3: 实现**

`Run(ctx)` 的流程（严格按 spec §7）：

1. `round = 0`；`items = []`；`ledger = NewFactLedger()`。
2. 调用 `planner.Plan(ctx, PlanRequest{Round: 0, ...})`：
   - 出错或输出无法解析 → 记 `planner.degraded` 事件，`items = SeedItems(cfg.Targets, cfg.Objective, 1)`；
   - 成功 → `ValidatePlan` 后取 `Accepted`。
3. 循环 `round <= cfg.Rounds`：
   - 选两条（排序键：① 存在未完成的复核 item 优先；② 目标未被探索过的优先；③ `Round` 更小优先；④ `ID` 字典序）；无可用 item → 结束。
   - 记 `round.started`；给每个 item 记 `item.assigned`（`AssignedTo` = Explorer ID，`Status = FrontierAssigned`）。
   - **并行**执行：每个 Explorer 先 `Reset(ctx)`（失败则该 item 记 `item.failed` 并跳过），再 `Investigate(ctx, item, brief)`；`brief = BuildExplorerBrief(cfg.Objective, cfg.Targets, item, DigestFacts(item, ledger.Facts()))`。
   - 等待本轮全部 Explorer 结束；`ctx.Done()` → 立即返回 ctx 错误。
   - 归并：先校验 `report.ItemID == item.ID`；`Investigate` 返回 error、或 item 不匹配 → 该 item `Status = FrontierFailed`、记 `item.failed`、**不入账**；通过 → `Status = FrontierDone`，`ledger.MergeClaims(round, item.ID, explorerID, report.Claims)`，记 `facts.merged`（含 `added`/`corroborated`/`contested`）。
   - 处理 `contradictions`：双方标 `Contested`，生成**一条**复核 item（`ReviewOf = [a, b]`，`Anchors = [a, b]`），记 `contested`。
   - 处理 `resolutions`：仅当该对事实 `Contested` 且已有复核 item 完成时生效 → 保留方 `Trust = FactCorroborated`、对方调用 `ledger.MarkRefuted(loser.ID, keeper.ID)` 置为 `FactRefuted`（`RefutedBy` 记对方），记 `resolved`。
   - 把报告里的 `next` 提案（≤3）经 `ValidatePlan` 校验后追加为新 item（`ParentID = 源 item.ID`）。
   - 记 `round.completed`。
   - 调用 Planner 生成下一轮 item（与第 2 步同样有 `planner.degraded` 降级）。
   - 终止判定：连续 2 轮所有 item 都 `failed` → 结束。
4. 返回 nil（**任何情况下都不返回"成功"语义**；成功只由 runner 在拿到 Verifier / backend completion 时决定）。

并行实现：`sync.WaitGroup` + 每 Explorer 一个 goroutine，结果写入按 index 对齐的切片；不使用 `errgroup`（仓库无该依赖）。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./... -run TestController`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add exploration_controller.go exploration_controller_test.go
git commit -m "feat(control): add exploration round controller"
```

---

### Task 6: Pi `Reset()` 与 session 适配器（`pi/session.go`、`session_agent.go`）

**Files:**
- Modify: `pi/session.go`
- Create: `pi/session_reset_test.go`
- Create: `session_agent.go`
- Create: `session_agent_test.go`

**Interfaces:**
- Consumes: Task 4 的 `PlanRequest`/`PlanOutput`/`ParsePlanOutput`；Task 3 的 `ParseExplorerReport`；Task 5 的 `Explorer` 接口。
- Produces:
  ```go
  // pi/session.go
  func (s *session) Reset(ctx context.Context) error   // 发送 {"type":"new_session"}

  // session_agent.go
  type EventSink func(AgentEvent)

  type SessionExplorer struct {
      Session      AgentSession
      ID           string
      WorkspaceDir string
      ToolLimit    int       // 每轮工具调用上限，超限即 Abort；0 表示不限制
      Sink         EventSink // 把 session 事件转给 runner 落库；可为 nil
  }
  func (e *SessionExplorer) ID() string
  func (e *SessionExplorer) Reset(ctx context.Context) error
  func (e *SessionExplorer) Investigate(ctx context.Context, item FrontierItem, brief string) (ExplorerReport, error)

  type SessionPlanner struct {
      Session AgentSession
      Sink    EventSink
  }
  func (p *SessionPlanner) Plan(ctx context.Context, req PlanRequest) (PlanOutput, error)

  type TurnOutcome struct {
      AssistantText string
      ToolCalls     []string
      Truncated     bool
  }
  func RunTurn(ctx context.Context, session AgentSession, prompt string, sink EventSink) (TurnOutcome, error)
  ```

- [ ] **Step 1: 写失败测试**

`pi/session_reset_test.go`（复用 `pi/session_test.go` 里已有的假 Pi 进程 harness）：
- `TestResetSendsNewSessionCommand`：假 Pi 收到 `{"type":"new_session"}` 并回 `{"type":"response","command":"new_session","success":true}` → `Reset` 返回 nil。
- `TestResetFailsWhenPiRejectsNewSession`：回 `success:false` → `Reset` 返回 error。

`session_agent_test.go`（用假 `AgentSession`）：
- `TestRunTurnReturnsAssistantText`：假 session 的 `turn_end` 事件里 assistant 消息含 `{"type":"text","text":"{\"next_items\":[]}"}` → `TurnOutcome.AssistantText` 等于该文本。
- `TestSessionExplorerReadsReportFromWorkspace`：`WorkspaceDir` 下写 `report.json` → `Investigate(ctx, item, brief)` 返回解析后的报告。
- `TestSessionExplorerFailsWhenReportMissing`：目录里没有 `report.json` → error 含 `report.json`。
- `TestSessionExplorerRejectsMismatchedReportItemID`：`report.json` 的 `item_id` 与传入 item 不符 → error 含 `item_id`。
- `TestSessionExplorerAbortsWhenToolLimitExceeded`：假 session 报告 5 次工具调用、`ToolLimit: 3` → session 被 Abort，`TurnOutcome.Truncated == true`。
- `TestRunTurnForwardsEventsToSink`：假 session 发 2 个事件 → sink 收到 2 次调用。
- `TestSessionPlannerParsesPlanJSON`：假 session 返回 assistant 文本 `{"next_items":[{"question":"q","target":"t"}]}` → `Plan` 得到 1 条 proposal。
- `TestSessionPlannerRejectsNonJSONAssistantText`：assistant 文本不是 JSON → error 含 `parse plan output`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./... -run "TestReset|TestRunTurn|TestSessionExplorer|TestSessionPlanner"`
Expected: FAIL（undefined symbols）

- [ ] **Step 3: 实现**

`pi/session.go`：
```go
// Reset starts a fresh Pi session so the next prompt begins with an empty context.
func (s *session) Reset(ctx context.Context) error {
    _, err := s.sendAndWait(ctx, map[string]any{"type": "new_session"})
    return err
}
```
并在 `session` 上加编译期断言 `var _ harness.ResettableSession = (*session)(nil)`。

`session_agent.go`：
- `RunTurn(ctx, session, prompt, sink)`：`session.Prompt(ctx, prompt)`；随后消费 `session.Events()`，直到收到 `agent_settled`（或 ctx 取消 / 流关闭）；**每收到一个事件先调用 `sink(event)`（非 nil 时）**，再继续处理；期间收集 `turn_end` 事件里 assistant 消息的文本部分（最后一条非空文本即 `AssistantText`）、工具调用名（`ToolCalls`）；`agent_settled` 的 `aborted` 为 true 时 `Truncated = true`。ctx 取消 → 返回 ctx 错误。
- `SessionExplorer.Investate(ctx, item, brief)`：`RunTurn(ctx, e.Session, brief, e.Sink)`；若 `e.ToolLimit > 0` 且 `len(outcome.ToolCalls) > e.ToolLimit` → `e.Session.Abort(ctx)` 并置 `Truncated = true`；随后读 `filepath.Join(e.WorkspaceDir, ReportFileName)`；读不到 → error（含 `report.json`）；读到 → `ParseExplorerReport(data, item)`（`item_id` 不符时 `ParseExplorerReport` 返回 error）。
- `SessionPlanner.Plan`：构造 Controller 简报文本（目标 + 目标面 + 有界事实账本 + open items + 上轮报告），`RunTurn` 后从 `AssistantText` 提取 JSON（允许 ```json 包裹，先剥围栏再解析），`ParsePlanOutput`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./... -run "TestReset|TestRunTurn|TestSessionExplorer|TestSessionPlanner"`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add pi/session.go pi/session_reset_test.go session_agent.go session_agent_test.go
git commit -m "feat(pi): add session reset and agent session adapters"
```

---

### Task 7: Runner 接线，替换旧探索路径（`runner.go`，删除 `state.go`/`frontier.go`）

**Files:**
- Modify: `runner.go`
- Modify: `types.go`（`RunSpec` 增加 `Exploration *ExplorationSpec`；删除 `FrontierKind`、`Frontier`、`ExplorationContext` 等旧类型）
- Modify: `runner_test.go`（现有 8 处 `RunSpec` 需要补 `Exploration` 与 3 个 agent，或改用展开后的 spec）
- Delete: `state.go`、`frontier.go`
- Rewrite: `exploration_test.go`

**Interfaces:**
- Consumes: Task 5 `Controller`/`ControllerEvent`；Task 6 `SessionExplorer`/`SessionPlanner`；Task 4 `ExplorationSpec`。
- Produces: `harness.RunSpec.Exploration *ExplorationSpec`；runner 发射 `exploration.<type>` 事件（类型见 spec §9.1）。

- [ ] **Step 1: 写失败测试**（重写 `exploration_test.go`），覆盖：

  - `TestRunRequiresExplorationSpec`：没有 `Exploration` → `Run` 返回 error（含 `exploration`）。
  - `TestRunRequiresExactlyTwoExplorers`：`ExplorerAgentIDs` 只有 1 个 → error（含 `explorer`）。
  - `TestRunRejectsUnknownAgentIDs`：`ControllerAgentID` 不在 `Agents` 里 → error。
  - `TestRunStartsExactlyThreeSessions`：Controller + 2 Explorer → 恰好 3 个 session 被创建，且未在 `ExplorerAgentIDs`/`ControllerAgentID` 中的 agent 不创建 session。
  - `TestRunEmitsExplorationEventsInOrder`：事件流里出现 `exploration.round.started` → `exploration.item.assigned` → `exploration.report.received` → `exploration.facts.merged` → `exploration.round.completed`，顺序单调递增。
  - `TestRunFailsClosedWhenExplorerReportIsInvalid`：假 Explorer 写坏报告 → run **不成功**，且 `exploration.facts.merged` 不包含任何事实。
  - `TestRunSuccessOnlyFromVerifier`：Verifier 返回 `Success: false` 而 Explorer 报告 `verdict: found` → `result.Verdict.Success == false`。
  - `TestRunSuccessOnlyFromBackendCompletion`：backend completion `Success: true` → `result.Verdict.Success == true`。
  - `TestRunStopsWhenVerifierSucceeds`：Verifier 在第 1 轮后返回成功 → 轮次数 ≤ 1（后续轮次不执行）。
  - `TestRunTerminatesOnBudgetExhaustion`：`Budget.MaxToolCalls` 很小 → run 结束且 `result.Verdict.Success == false`，错误含 `budget`。
  - `TestRemovedPublicSymbolsAreGone`：编译期即验证（删除后测试文件不再引用 `harness.ExplorationState` 等）。
  - `TestExpandExplorationAgentsCreatesThreeRoles`：只给 1 个 agent spec → 返回 3 个 spec（ID 分别为 `<id>-controller`、`<id>-explorer-1`、`<id>-explorer-2`）与 `ExplorationSpec{ControllerAgentID, ExplorerAgentIDs}`；Controller spec 的 `Pi.Tools` 被强制为 `["read"]`。
  - `TestExpandExplorationAgentsKeepsExplicitSpec`：已有 `RunSpec.Exploration` 时原样返回，不展开。
  - `TestRunForcesControllerReadOnlyTools`：显式给出 Controller spec 且其 `Pi.Tools` 含 `bash` → runner 仍以只读工具启动 Controller。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./... -run "TestRun"`
Expected: FAIL（`RunSpec.Exploration` 不存在等）

- [ ] **Step 3: 实现**

角色展开 helper（放在 `types.go` 或新文件 `exploration_spec.go`，实现者自选，但签名固定）：

```go
// ExpandExplorationAgents returns the agent specs and exploration spec for a run.
// When spec.Exploration is already set the inputs are returned unchanged.
// Otherwise exactly one agent spec is expanded into controller + two explorers.
func ExpandExplorationAgents(spec RunSpec) (RunSpec, error)
```

展开规则：单个 agent spec → 3 个 spec，ID 为 `<id>-controller` / `<id>-explorer-1` / `<id>-explorer-2`；
Controller 的 `Pi.Tools` 强制为 `["read"]`；`spec.Exploration = &ExplorationSpec{ControllerAgentID: "<id>-controller", ExplorerAgentIDs: [...], Rounds: 0}`（`Rounds <= 0` 由 runner 取默认 6）。

`types.go`：
```go
type RunSpec struct {
    Task          Task            `json:"task" yaml:"task"`
    Target        Target          `json:"target,omitempty" yaml:"target"`
    Agents        []AgentSpec     `json:"agents" yaml:"agents"`
    Exploration   *ExplorationSpec `json:"exploration,omitempty" yaml:"exploration"`
    Budget        Budget          `json:"budget,omitempty" yaml:"budget"`
    Backend       Backend         `json:"-" yaml:"-"`
    Verifier      Verifier        `json:"-" yaml:"-"`
    SecretEnv     []string        `json:"secret_env,omitempty" yaml:"secret_env"`
    WorkspaceRoot string          `json:"workspace_root,omitempty" yaml:"workspace_root"`
}
```
删除 `FrontierKind`、`Frontier`、`ExplorationContext`、`FrontierItem`（旧版）、`ExplorationState`、`ActionAttempt`、`ExplorationObservation` 及 `NewExplorationState`/`ReduceExploration`/`ProjectExplorationFrontier`/`BuildExplorationContext`/`FormatExplorationContext`。

`runner.go`：
- `validateSpec`：要求 `spec.Exploration != nil`；`ControllerAgentID` 必须命中 `Agents`；`len(ExplorerAgentIDs) == 2` 且都命中 `Agents`；`Rounds <= 0` 时取默认 6。
- 会话创建：只为 Controller 与两个 Explorer 建 session（各自 workspace 目录：`sessions/<agentID>`）；Controller 的 `AgentSpec.Pi.Tools` 强制为 `["read"]`（若用户未显式指定）。
- 旧的"每 agent 一个 goroutine + `runOneSession`"流程替换为：Controller goroutine 跑 `Controller.Run(ctx)`。
- **事件/日志转发**：session 的 `Events()` 只能有一个消费者（`RunTurn` 已在消费），因此删除旧的每 session 事件转发 goroutine，改为把 `Sink` 传给 `SessionExplorer`/`SessionPlanner`：sink 内把 `AgentEvent` 转成 `harness.Event{Type: event.Type, Source: "agent", Data: event.Data}` 并调用 `r.emitEvent`；`Logs()` 通道仍由 runner 单独起 goroutine 转发（与事件通道互不冲突）。
- `ControllerEvent` → `harness.Event{Type: "exploration." + ev.Type, Source: "control", Data: mustJSON(ev.Data)}`，用现有 `r.emitEvent` 落库。
- 每轮结束后调用 `spec.Verifier.Verify`（若配置）；成功 → `run.finish` 成功并 cancel Controller ctx；失败 → 继续下一轮（不是立刻判失败）。
- backend completion 成功 → 同样立即终止。
- 预算检查沿用 `checkLiveBudget`；Controller 上下文中断后 run 以"无权威判定"失败结束。
- 删除 `runOneSession` 里与旧探索投影有关的代码（`NewExplorationState`/`ReduceExploration`/`BuildExplorationContext`/`Steer` 调用全部移除）。

- **角色展开**：`Run` 入口调用 `ExpandExplorationAgents(spec)`；`spec.Exploration != nil` 时原样使用，否则按上表展开。展开后 `spec.Agents` 只保留被引用的 3 个 spec。
- **Controller 只读强制**：无论 spec 怎么写，Controller 会话的 `Pi.Tools` 都被覆盖为 `["read"]`（`pi.BuildCommand` 收到 `Tools: []string{"read"}`）。
- **CLI / 服务器无需改动**：展开发生在 `runner.Start` 入口，`internal/cli` 与 `server` 传入的单 agent spec 会自动展开成 3 个角色；现有 YAML 与 HTTP 请求体保持兼容。
- 删除 `state.go`、`frontier.go`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./... && go vet ./... && go build ./cmd/harness`
Expected: 全部通过（`go test ./...` 会同时验证旧公开符号已移除、`runner_test.go` 已补齐 `Exploration`、`internal/cli` 的旧 YAML 仍能构造出合法 spec）

- [ ] **Step 5: 提交**

```bash
git add -A
git commit -m "feat(control): replace single-agent exploration with controller rounds"
```

---

### Task 8: Web 轮次视图（`web/`）

**Files:**
- Modify: `web/src/utils/exploration.ts`
- Modify: `web/src/utils/exploration.test.ts`
- Modify: `web/src/components/ExplorationTimeline.tsx`
- Modify: `web/src/pages/RunDetailPage.tsx`
- Modify: `web/src/i18n/messages.ts`

**Interfaces:**
- Consumes: Task 7 落库的 `exploration.*` 事件（payload 形状见 spec §9.1）。
- Produces: `web/src/utils/exploration.ts` 导出
  ```ts
  export interface ExplorationRound {
    round: number
    items: ExplorationRoundItem[]
    facts: ExplorationFact[]
    contested: [string, string][]
    completed: boolean
  }
  export interface ExplorationRoundItem {
    id: string
    question: string
    target_hint?: string
    assigned_to?: string
    verdict?: string
    valid?: boolean
    claims?: number
    next?: number
    truncated?: boolean
  }
  export interface ExplorationFact {
    id: string
    claim?: string
    trust?: string
    contested?: boolean
    sources?: string[]
  }
  export function extractExplorationRounds(events: EventRecord[]): ExplorationRound[]
  ```

- [ ] **Step 1: 写失败测试** `web/src/utils/exploration.test.ts`，覆盖：

  - `TestExtractExplorationRoundsPairsEventsByRound`：给定 `exploration.round.started{round:1}`、`exploration.item.assigned{round:1,item_id:"i-1"}`、`exploration.report.received{round:1,item_id:"i-1",verdict:"found"}`、`exploration.round.completed{round:1}` → 得到 1 个 round，items 含 `i-1`，`verdict === "found"`。
  - `TestExtractExplorationRoundsSortsByRoundDescending`：round 1、2 → 返回顺序为 2、1。
  - `TestExtractExplorationRoundsCollectsFacts`：`exploration.facts.merged{round:1,added:["f-1"],corroborated:["f-2"]}` → round 的 facts 含 `f-1`、`f-2`。
  - `TestExtractExplorationRoundsMarksContested`：`exploration.contested{round:1,a:"f-1",b:"f-2"}` → `contested` 含 `["f-1","f-2"]`。
  - `TestExtractExplorationRoundsIgnoresUnknownEvents`：无关事件 → 返回空数组。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npm test -- exploration.test.ts`
Expected: FAIL

- [ ] **Step 3: 实现**

- `web/src/utils/exploration.ts`：改为按 `exploration.*` 事件解析出 `ExplorationRound[]`；保留文件里其他仍被引用的导出（若旧导出无人引用则一并删除，并同步删掉 `RunDetailPage.tsx` 的引用）。
- `web/src/components/ExplorationTimeline.tsx`：每轮一个卡片，轮内两条泳道（E1/E2，按 `assigned_to` 分列），item 显示 question / verdict / claims 数 / next 数；底部显示本轮新增事实与 contested 高亮。
- `web/src/i18n/messages.ts`：新增/更新中英文案（键名沿用 `run.exploration.*` 前缀，新增 `run.exploration.round`、`run.exploration.explorer`、`run.exploration.verdict`、`run.exploration.facts`、`run.exploration.contested` 等）。
- `web/src/pages/RunDetailPage.tsx`：把 `ExplorationTimeline` 换成新的 `extractExplorationRounds` 数据源。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd web && npm run lint && npm run typecheck && npm test && npm run build`
Expected: 全部通过

- [ ] **Step 5: 提交**

```bash
git add web
git commit -m "feat(webui): render exploration rounds and facts"
```

---

### Task 9: 文档、全量验证与真实 TSec 跑通

**Files:**
- Modify: `docs/architecture.md`
- Modify: `README.md`（如有探索控制章节）
- Create: `docs/superpowers/verification/2026-10-21-exploration-expansion.md`（验证证据）

**Interfaces:**
- Consumes: Task 1–8 的全部产物。
- Produces: 验证记录文档。

- [ ] **Step 1: 更新文档**

`docs/architecture.md` 的"Exploration control"章节改写为：Controller 轮循环、事实账本、冲突复核、注入三段式、事件类型表；删除关于 `ExplorationState`/`Frontier` 旧投影的描述。

- [ ] **Step 2: 全量 Go 验证**

Run: `go test ./... && go test -race ./... && go vet ./... && go build ./cmd/harness`
Expected: 全部通过

- [ ] **Step 3: 前端验证**

Run: `cd web && npm run lint && npm run typecheck && npm test && npm run build`
Expected: 全部通过

- [ ] **Step 4: 真实 TSec 跑通**

启动 VPN（使用现有的 `task_LgIJispUlOB_vpn_config.ovpn`），确认 `tun0` 就绪；然后用新控制环跑一个已知可解的挑战（`a-01`）。跑前检查宿主机 CPU/内存余量与现存容器名，确认容器名按 run 身份派生、无冲突。

Expected: run 结束，事件流里能看到 ≥2 轮 `exploration.round.started`，每轮 2 条 `exploration.item.assigned`，并且 `result.json` / `result.Verdict` 记录了后端判定结果（成功与否都要如实记录，不得由报告内容推断）。

- [ ] **Step 5: 写验证记录**

`docs/superpowers/verification/2026-10-21-exploration-expansion.md` 记录：Go/前端命令与结果、TSec run id 与判定、轮次数、事实条数、遇到的失败与处理。**不得写入 token、密码、flag 明文。**

- [ ] **Step 6: 提交并推送**

```bash
git add -A
git commit -m "docs(architecture): describe exploration expansion control loop"
git push
```

---

## 附：任务依赖顺序

```
Task 1 (spike)
  └─> Task 6 (Pi Reset + 适配器)
Task 2 (facts) ─┐
Task 3 (explorer)├─> Task 5 (controller) ─> Task 7 (runner) ─> Task 8 (web) ─> Task 9 (验证)
Task 4 (planner)─┘
```

Task 2/3/4 相互独立可并行；Task 5 依赖 2/3/4；Task 6 依赖 Task 1；Task 7 依赖 5 与 6。
