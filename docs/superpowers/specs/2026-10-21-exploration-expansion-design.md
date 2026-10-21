# 探索扩散架构设计：单 Controller + 两个短期 Explorer

- 日期：2026-10-21
- 状态：待评审（builder check 后交维护者确认）
- 影响面：`harness` 公开 API、runner 控制环、Pi session、Web 可观测性

---

## 1. 意图与为什么

当前 Red Harness 的探索控制是**单 Agent 长上下文**：`runner.go` 启动一个 Pi session，打一次初始 prompt，
之后每个 `turn_end` 归约成 `ExplorationState`，投影出模板化的 `Frontier`，用 `Steer` 把一个 focus
注入同一会话。这个骨架已经在真实 TSec 挑战上跑通（`a-01`，score=500），但它有三种结构性疾病：

| 病 | 症状 | 根因 |
|---|---|---|
| 注意力稀释 | 一个上下文里同时装着目标、多个假设和大量工具输出，模型每轮都在重新权衡"现在该干什么" | 语义状态与噪声共用一个上下文窗口 |
| 重复劳动 | 上下文压缩或重建后，把已经验证过的东西重新验证一遍 | 没有跨上下文的共享记忆 |
| 缺少广度 | 一次一个动作、线性深挖，容易错过分支 | 探索宽度是并发属性，不是上下文属性 |

这三件事分别对应三个最小机制，且只有三个：

1. **共享事实账本**：跨上下文存活的唯一真相，治重复劳动。
2. **短命 Explorer ×2 并行**：每轮只干一件事、全新上下文，治注意力稀释并提供广度（从起点扩散）。
3. **单一 Controller**：唯一的分配者与裁决者，避免重复派活、无主分歧和无人负责停止。

第四个常见组件——独立的"生成 frontier 的 Agent"——被合并进 Controller：
Controller 本身就是一个**只规划、不探测**的 LLM session，它既产出下一批探索项，也裁决冲突事实。
它没有工具权限，因此不会自己去做探索、不会与 Explorer 抢活、也不会污染事实来源。

### 1.1 设计原则

- **成功只能由 Verifier / backend completion 判定。** Controller、Explorer、事实账本、工具输出，
  任何一环都不得产生"任务成功"结论。
- **语义判断归 LLM，机械不变量归代码。** 下一批探索什么、两条冲突断言谁成立，是语义问题，交给
  Controller；"每轮恰好 2 条""anchor 必须来自 corroborated 事实""报告必须属于被分配的 item"是机械
  不变量，由代码强制，LLM 说了不算。
- **一轮一个焦点。** 每个 Explorer 每轮只认领 1 个 item，只注入"目标 + 1 条 item + 有界事实摘要"。
  不注入历史对话、不注入原始 observation 洪水。
- **失败即止，不静默兜底。** 结构不合法的报告让对应 item 判 failed 且不入事实库（fail-closed）。
- **SQLite 事件流是唯一审计源。** 探索状态全部以事件形式落库，不新增第二份可变状态表。

---

## 2. 目标 / 非目标 / 反目标

### 2.1 目标

1. 一次 run 内由 Controller 驱动**多轮**探索；每轮恰好 2 条 item，并行分派给 2 个 Explorer。
2. 每个 Explorer 每轮使用**全新上下文**（Pi `new_session`），注入内容固定为三段。
3. 跨轮共享的**事实账本**（fact ledger），事实带信任等级，并能表达"支持/反驳"关系。
4. 冲突按已定策略 A 处理：双方都保留、标记 `contested`，下一轮做一次复核，复核后裁决。
5. Explorer 必须产出**结构化报告**；报告不合法则该 item 判 failed，不污染事实库。
6. 全部控制周期在 SQLite 事件流可审计，并在 Web 后台可视化。

### 2.2 非目标（v1 不做）

- 不做独立 Generator Agent（规划已在 Controller 内）。
- 不做向量检索 / RAG；事实账本 + 文件系统就是认知面。
- 不做新的 SQLite 物化表（`exploration_items` / `exploration_facts`）；一切走事件流，需要事后查询时再物化。
- 不做每个 item 独立的验证脚本；item 级验证由 Explorer 报告中的证据承担，目标级成功由 Verifier 承担。
- 不做跨 run 的知识复用、不做多挑战调度、不做 agent 角色可配置化（固定 1 Controller + 2 Explorer）。
- 不做复杂 UI；复用现有 Exploration Tab，改成"轮次 → 2 泳道 → 报告 → 事实"的事件驱动视图。

### 2.3 反目标（做到这些算失败）

- Controller 或 Explorer 的输出、事实账本里的任何一条 corroborated 事实，被用来判定"任务成功"。
- 单轮给 Explorer 注入多条 item 或大段历史，导致注意力稀释回归。
- 同一 item 被两个 Explorer 同时认领，或两条内容等价的 item 并行执行。
- 因为格式问题静默丢弃事实、或者把不合法的报告当成"没有发现"。
- 新旧两套控制语义并存（旧 `ExplorationState` 投影与新 Controller 同时在跑）。

---

## 3. 约束

来自 `AGENTS.md` 与本仓库既有实践：

- 公开库不依赖 CLI / HTTP server 可用。
- 只有 `Verifier` 或 backend completion 能产生成功判定。
- Pi RPC 严格 LF JSONL framing；`agent_settled` 才是完成，`agent_end` 不是。
- SQLite 是规范审计存储；写入前脱敏（`BENCHMARK_TOKEN` 等绝不明文落库）。
- 容器不得接触 Docker socket；只读根文件系统、`no-new-privileges`、drop caps、资源限额。
- 先写测试再实现；负面/失败关闭用例覆盖畸形输入、取消、进程退出、校验错误与清理。
- Conventional Commits 带 scope；实现并通过验证后自动 commit + push。

### 3.1 授权目标边界（信任边界，必须由代码强制）

- item 的 `TargetHint` 必须匹配 `Task.Targets` 中的 `ID` 或 `Addresses` 条目，
  或由这些条目派生的 host / port / path（同一目标的子路径与端口视为授权）。
- 越界 item 在校验阶段被**丢弃**并记一条诊断事件；不判整轮失败，不影响同轮另一条 item。
- Controller 的简报里只出现授权目标；事实账本与报告文本都是不可信输入，
  它们**不能**扩大授权范围（授权只来自 `Task.Targets`，不来自任何模型输出）。
- Explorer 拥有 bash 与网络工具，这是事实上的能力边界；因此"注入的 item 不得指向未授权目标"
  必须由上面的校验保证。

运行环境约束：

- 单 run 最多 3 个 Pi 进程（2 Explorer + 1 Controller），每进程内存 2g；启动前需确认宿主机余量。
- 容器名必须由 run 身份派生（SHA-256 of resolved run path，前 24 hex），保证并行/续跑不冲突。

---

## 4. 架构总览

```
                    ┌──────────────────────────────────────────────┐
                    │ Controller（LLM session，无工具，只规划/裁决）  │
                    └──────────────────────────────────────────────┘
                       ▲ brief(目标/事实账本/open items/上轮报告)      │
                       │ planner_output(2 条 next_items + 裁决)         │
                       ▼                                            │
  ┌──────────────── Controller 状态（进程内，事件流落库）────────────┐
  │  facts[]  items[]  rounds[]                                    │
  └───┬──────────────────────────────────────────────┬─────────────┘
      │ brief(item_i + facts digest)                 │ 同一轮并行
      ▼                                              ▼
┌──────────────┐                            ┌──────────────┐
│ Explorer E1  │                            │ Explorer E2  │
│ 全新上下文    │                            │ 全新上下文    │
│ 1 条 item     │                            │ 1 条 item     │
│ → report.json │                            │ → report.json │
└──────┬───────┘                            └──────┬───────┘
       └───────────────┬────────────────────────────┘
                       ▼
              校验（fail-closed）→ 事实账本归并 → 冲突标记
                       │
                       └──→ 下一轮
```

时序（一轮）：

1. Controller 收到 brief（目标、目标面、事实账本、open items、上轮报告），输出恰好 2 条 next_items
   （以及可选的冲突/裁决）。
2. Controller 对 E1、E2 各自 `Reset()`（Pi `new_session`）→ `Prompt(brief_i)` → 等待 `agent_settled`。
3. Controller 读各自的 `report.json`，校验并归并进事实账本，更新 item 状态。
4. Controller 进入下一轮，直到终止条件。

**关键点**：Controller 的上下文每轮也是全新的。它之所以是"唯一裁决者"，不是因为它记得最多，
而是因为**所有跨轮状态都在外部账本里**，它的每一轮输入都是账本的有界投影。

---

## 5. 已定决策

| # | 决策 | 选择 | 理由 |
|---|---|---|---|
| D1 | 角色数量 | 1 Controller + 2 Explorer | 两路并行 = 最小的广度来源；再加只会稀释预算 |
| D2 | Controller 形态 | LLM session，**无工具**，只规划/裁决 | 规划与裁决是语义问题；不加工具保证它不自己探测、不与 Explorer 抢活 |
| D3 | 上下文策略 | 每轮 `new_session` 全新上下文 | 治注意力稀释；跨轮记忆靠事实账本，不靠上下文 |
| D4 | 冲突策略 | A：双方保留 + 标 `contested` + 下一轮复核 + 复核后裁决 | 不静默覆盖；每个冲突对最多复核一次 |
| D5 | 落地方式 | **替换**现有单 Agent 探索路径 | 两套控制语义并存是长期成本；旧投影被新机制包含 |
| D6 | 事实信任等级 | `observed` / `corroborated` / `refuted`（**去掉** `verified`） | `verified` 混淆了事实级与目标级；目标级成功只由 Verifier 判定 |
| D7 | Frontier item 字段 | 不存 `Priority`，排序是裁决时的计算结果 | 减少持久状态；排序规则集中在一处 |
| D8 | 存储 | 全部走事件流，不新增 SQLite 表 | SQLite `events` 表已是唯一审计源 |
| D9 | Explorer 产出通道 | 写 `/workspace/report.json`，Controller 从宿主读文件 | 比解析聊天文本稳健；也便于人工排查 |
| D10 | 每轮 item 数 | 恰好 2（不可配置） | "扩散"的核心节拍；可配置会引出"到底该几个"的争论 |
| D11 | 角色配置表达 | `RunSpec.Agents` 继续作为 agent spec 池；新增 `RunSpec.Exploration{Rounds, ControllerAgentID, ExplorerAgentIDs}`，按 ID 引用其中 3 个 spec | 不改动 `Agents` 既有语义；未引用的 spec 不启动容器；角色身份显式可校验 |
| D12 | Controller 权限 | Controller 只读：`PiSpec.Tools = ["read"]`（无 bash、无写）；Explorer 保留 bash/write | "Controller 不自己探测"必须由容器权限强制，不能只靠 prompt |
| D13 | Verifier 调用时机 | 每轮结束后调用一次（非 backend Verifier）；成功即终止并取消在跑的 Explorer | 让"成功即停"有明确执行点；成本可控 |
| D14 | 授权目标边界 | item 的 target 必须匹配 `Task.Targets` 的 id/address，或由其派生的 host/port/path；越界 item 在校验阶段丢弃并记诊断 | Controller 输出与账本文本都不可信，必须由代码兜住安全边界 |

---

## 6. 数据结构契约

以下是设计层面的契约。字段名以本文为准，实现者可调整命名，但**语义与约束不可放宽**。

### 6.1 FrontierItem（待探索项）

```go
type FrontierItem struct {
    ID             string         `json:"id"`
    ParentID       string         `json:"parent_id,omitempty"`
    Question       string         `json:"question"`        // 一句可证伪的问题
    TargetHint     string         `json:"target_hint"`     // 目标 id 或地址
    ExpectedEvidence string       `json:"expected_evidence"` // 期望看到的证据形态
    Anchors        []string       `json:"anchors,omitempty"`  // 只能引用 corroborated 事实 ID
    ReviewOf       []string       `json:"review_of,omitempty"` // 非空 = 冲突复核 item，anchors 可引用 contested 事实
    Status         FrontierStatus `json:"status"`           // open|assigned|done|failed
    AssignedTo     string         `json:"assigned_to,omitempty"` // Explorer session id
    Round          int            `json:"round"`
}
```

约束：

- `Question` 非空、`TargetHint` 非空（没有已知目标时不生成 item）。
- `Anchors` 中的每个 ID 必须存在于账本且 `trust == corroborated`，否则该 anchor 被丢弃
  （**丢弃 anchor，不丢弃 item**）。
- **例外**：`ReviewOf` 非空的冲突复核 item 允许 anchor 处于 `contested` 的事实（它要复核的正是这两条）。
  除复核 item 外，anchor 规则不放宽。
- `Status` 终态为 `done` / `failed`；`assigned` 由代码设置，LLM 不得指定。

### 6.2 Fact（事实账本条目）

```go
type Fact struct {
    ID        string    `json:"id"`
    Claim     string    `json:"claim"`     // 一句可证伪断言
    Evidence  string    `json:"evidence"`  // 有界摘要，≤400B
    Source    string    `json:"source"`    // "sessionID#itemID"
    Sources   []string  `json:"sources"`   // 断言过该事实的不同 session id
    Trust     FactTrust `json:"trust"`     // observed|corroborated|refuted
    Contested bool      `json:"contested"`
    Targets   []string  `json:"targets"`
    RefutedBy []string  `json:"refuted_by,omitempty"` // 反驳它的事实 ID
}
```

约束：

- ID 由代码生成（`f-<round>-<seq>`），不信任 LLM 提供的 ID。
- `Evidence` 截断到 400B；`Claim` 截断到 300B。
- `Sources` 去重；`corroborated` 要求 **≥2 个不同 session**。
- `refuted` 只允许来自：① 被 `corroborated` 事实显式反驳；② 冲突复核后被裁决输的一方。
- 账本上限：200 条事实、100 条 item、每目标 ≤ 40 条事实。超出时按"最新 + corroborated 优先"裁剪。

### 6.3 ExplorerReport（探索者唯一结构化产出）

```json
{
  "item_id": "i-3",
  "verdict": "found | not_found | inconclusive",
  "claims": [
    {"claim": "...", "evidence": "...", "targets": ["t1"], "supports": ["f-1"], "refutes": ["f-2"]}
  ],
  "next": [
    {"question": "...", "target": "t1", "expected_evidence": "...", "anchors": ["f-1"]}
  ],
  "commands": ["nmap -sV ..."],
  "errors": []
}
```

校验规则（fail-closed）：

- 文件缺失 / 非法 JSON / 不是对象 → **item 判 failed，该报告不入账**（不影响同轮另一条 item）。
- `item_id` 与被分配 item 不符 → item 判 failed，不入账。
- `verdict` 不在枚举内 → item 判 failed，不入账。
- `claims` 中缺 `claim` 或缺 `evidence` 的条目 → **只丢该条 claim**，不影响其他 claim。
- `next` 最多保留 3 条；`question` 为空的丢弃。
- `supports` / `refutes` 引用的 fact ID 不存在 → 丢弃该引用（并记一条诊断），不判失败。
- 报告有效但 session 被中止（超时/超工具上限）→ 仍然入账，但 item 标记 `done` 且事件里带 `truncated: true`。

### 6.4 Controller 的 planner 输出

```json
{
  "next_items": [
    {"question": "...", "target": "t1", "expected_evidence": "...", "anchors": ["f-1"]}
  ],
  "contradictions": [{"a": "f-3", "b": "f-7", "note": "..."}],
  "resolutions":   [{"a": "f-3", "b": "f-7", "keep": "f-3", "note": "..."}]
}
```

代码侧的机械校验（LLM 说了不算）：

- `next_items` 必须给出 **2 条**；少给则用 open items 的确定性排序补齐；多于 2 条只取前 2 条。
  bootstrap 阶段（还没有任何 open item）若 Controller 输出损坏，用 §7.1 的确定性种子 item 启动，
  事件标记 `planner_degraded`。
- item 的 target 必须通过 §3.1 的授权目标校验；越界 item 被丢弃并记诊断事件（**不判整轮失败**）。
- 每条 item 校验：`question`/`target` 非空；`anchors` 只保留存在且 `corroborated` 的事实 ID；
  与已有 open item 或最近完成的 item **归一化后重复**（小写 + 去标点 + 去空白）则丢弃该条。
- `contradictions` 只能作用于账本中已有的事实对；标记后双方 `contested = true`，
  并且**下一轮生成恰好一条复核 item**（"X 还是 Y 成立？"），两个 claim 都作为 anchors。
  同一对事实最多复核一次。
- `resolutions` 只在"该对事实处于 contested 且其复核 item 已完成且复核报告支持其中一方"时生效；
  生效后保留方 → `corroborated`，对方 → `refuted`。复核 inconclusive 时双方维持 contested。

---

## 7. 控制循环

伪代码（实现语言不限）：

```
ledger  = newLedger()
items   = []
round   = 0

plan = controllerCall(objective, targets, ledger, items, reports=[])   // bootstrap
items += validateItems(plan.next_items, ledger)

while round < MaxRounds:
    round += 1
    batch = pickTwo(items)            // 确定性选择，见 §7.1
    if len(batch) < 1: break

    parallel for each (explorer, item) in batch:
        explorer.Reset()              // Pi new_session → 全新上下文
        explorer.Prompt(brief(objective, item, digest(ledger, item)))
        wait agent_settled

    for each (explorer, item) in batch:
        report = readReport(explorer.workspace + "/report.json")
        ok, claims = validate(report, item)
        if !ok:
            item.status = failed
            emit item.failed
            continue
        item.status = done
        mergeClaims(ledger, claims, explorer.sessionID)   // §7.2
        absorbNext(ledger, claims, item)                  // 提案 → 候选 item

    plan = controllerCall(objective, targets, ledger, items, reports=reportsOf(batch))
    items += validateItems(plan.next_items, ledger)
    applyContradictions(ledger, plan.contradictions)
    applyResolutions(ledger, plan.resolutions)

    emit exploration.round.completed
    if shouldStop(ledger, items): break
```

### 7.1 选两条（确定性）

排序键（全部由代码计算）：

1. 处于 `contested` 且尚有复核 item 未完成的，优先；
2. 未被任何 Explorer 认领过的目标优先（广度）；
3. 轮次更早的优先（不饿死老方向）；
4. 同分时按 item ID 字典序（保证可复现）。

一次最多取 2 条；任何时刻同一个 item 只能被一个 Explorer 认领。

批次规模的容许规则（避免"必须凑够 2 条"逼出编造）：

- 有 2 条可用 item → 本轮并行 2 条。
- 只有 1 条可用 item → 本轮执行这 1 条（单条轮次是合法的），事件里记录 `batch_size: 1`。
- 0 条可用 item 且 Controller 也没有产出新 item → 结束（§8）。
- bootstrap 时 Controller 输出损坏且没有任何 open item → 用**确定性种子 item** 启动：
  每个授权目标一条"摸清该目标的攻击面与可达服务"，外加一条任务意图相关的初始问题；
  事件标记 `planner_degraded`。种子 item 与 Controller 产出的 item 在账本里没有区别。

### 7.2 归并事实（机械，不依赖文本相似度）

Explorer 报告里的 claim 通过**显式引用**驱动归并：

- claim 若 `supports: ["f-1"]` 且 f-1 存在 → 把该 Explorer session 记为 f-1 的第二个来源
  → f-1 升级为 `corroborated`；该 claim 本身不再新增事实（它是 f-1 的复证）。
- claim 若 `refutes: ["f-2"]` 且 f-2 存在 → **不立即改判**（策略 A：绝不静默覆盖）：
  - 被反驳方无论当前信任等级，一律降级为 `contested`（不回退成 `refuted`）；
  - 新 claim 保持 `observed`（它只有 1 个来源，不能自己变成 `corroborated`）；
  - 双方标 `contested`，并生成**一条**复核 item（`ReviewOf = [f-2, 新事实 ID]`）。
  - 复核完成后由 Controller 的 `resolutions` 裁决：保留方 → `corroborated`，对方 → `refuted`。
  - 由此不变式成立：`corroborated` 永远意味着 ≥2 个不同 session；`refuted` 永远来自一次显式裁决。
- claim 若既不 supports 也不 refutes 已有事实 → 新增事实，`trust = observed`。

冲突策略 A 的完整表达：**任何时刻都不静默覆盖**。被反驳的事实要么转 `refuted`（有明确胜方），
要么与对手并存且标 `contested`（等待一次复核）。

### 7.3 注入简报（Explorer 与 Controller 的 prompt 内容）

Explorer 简报固定五段，顺序固定：

```
Task objective: <objective>
Authorized targets: <id / addresses>

Your assigned item:
  id: <item id>
  question: <question>
  target: <target hint>
  expected evidence: <expected evidence>
  known facts (corroborated only, may rely on these):
    - [f-3] <claim> (evidence: <evidence ≤200B>)
  ruled out / contested (do not repeat these):
    - [f-7 contested] <claim>

Rules:
  1. Investigate ONLY the assigned question. One question, one investigation.
  2. You may use the tools. Do not attempt to submit or claim task success.
  3. Write your final report to /workspace/report.json using exactly this schema: {...}
  4. "not_found" and "inconclusive" are valid and useful results. Do not invent findings.
  5. Write nothing else to /workspace/report.json.
```

事实摘要的边界：只包含与该 item 的 `TargetHint` 或 `Anchors` 相关的事实；上限 8 条；
每条 claim ≤200B；`refuted` 事实**不给**（避免把已被证伪的东西当线索），`contested` 事实给但标注。

Controller 简报固定四段：目标与目标面 / 事实账本（有界）/ open items 与最近完成 item（有界）/ 上轮两份报告（有界）。
Controller **没有工具**，它的输出只能是 §6.4 的 JSON。

---

## 8. 终止与预算

终止条件（任一满足即停）：

- `Verifier` 或 backend completion 判定成功 → 立即停（唯一成功路径）。
  非 backend 的 `Verifier` 在**每轮结束后**调用一次（D13）；一旦成功，取消仍在运行的 Explorer 并结束 run。
  TSec 场景的成功来源是 backend completion：Explorer 提交 flag 后由 backend observe 观察到完成。
- `MaxRounds`（默认 6，可配置）用尽。
- 全局预算耗尽：`WallTime` / `MaxTokens` / `MaxModelCalls` / `MaxToolCalls` / `MaxCostUSD`。
- 连续 2 轮两条 item 全部 failed（避免格式问题导致的空转）。
- 账本无可用 item 且 Controller 也没有产出新 item。

每轮每 Explorer 的工具调用上限（默认 40）：超限即 `Abort` 该 session，按 §6.3 的"截断但入账"处理。

---

## 9. 可观测性

### 9.1 事件（全部落 SQLite `events` 表）

| 事件类型 | 数据 |
|---|---|
| `exploration.round.started` | `{round}` |
| `exploration.items.planned` | `{round, items:[{id, question, target, anchors}]}` |
| `exploration.item.assigned` | `{round, item_id, assigned_to, question, target}` |
| `exploration.report.received` | `{round, item_id, assigned_to, verdict, valid, claims, next, truncated, error}` |
| `exploration.facts.merged` | `{round, added:[fact_ids], trust_changes:[{fact_id, from, to}], corroborated:[fact_ids]}` |
| `exploration.contested` | `{round, a, b, source}` |
| `exploration.resolved` | `{round, a, b, kept}` |
| `exploration.round.completed` | `{round, open_items, facts, verdicts}` |

### 9.2 Web 后台

现有 Exploration Tab 从"turn → state → frontier"改为"轮次 → 两条泳道（E1/E2）→ item 卡片 → 报告 → 事实"：

- 顶部：轮次进度、事实计数（observed/corroborated/refuted/contested）、当前是否已判定成功。
- 每个轮次两列泳道，展示 item 问题、verdict、命令数、发现的 claim。
- 事实区：表格展示 claim / trust / 来源 session / 是否 contested。
- contested 的事实对高亮，并显示其复核 item 与裁决结果。

数据全部来自事件流（前端已有 `extractExplorationCycles` 的模式，改为按 session + round 配对）。

---

## 10. 代码落点

| 文件 | 动作 |
|---|---|
| `exploration_controller.go`（新） | 轮循环、选两条、终止判定、事件发射 |
| `planner.go`（新） | `Planner` 接口 + 基于 Pi 的 LLM Controller 实现（只读工具，D12）+ Controller 简报构造 + planner 输出校验与降级 |
| `explorer.go`（新） | Explorer 简报构造、报告读取与校验 |
| `facts.go`（新） | 事实账本、信任状态机、归并与冲突处理 |
| `types.go` | 新类型（`Fact`/`FactTrust`/`FrontierItem` 扩展/`ExplorerReport`）；`AgentSession` 增加**可选**接口 `ResettableSession{ Reset(ctx) error }`；`RunSpec` 增加 `Exploration *ExplorationSpec{Rounds, ControllerAgentID, ExplorerAgentIDs}`（D11），runner 校验：Controller 恰好 1 个、Explorer 恰好 2 个 |
| `pi/session.go` | 实现 `Reset()`：发送 `{"type":"new_session"}` 并等待响应（Pi 1.0.4 已确认支持，见 `docs/rpc-commands.md` 的 `new_session`） |
| `runner.go` | 用 Controller 轮循环取代"prompt 一次 + steer"的生命周期；session 变为"每轮 reset + prompt" |
| `state.go`、`frontier.go` | **删除**（旧模板化投影被新机制取代）——公开 API 破坏性变更，见 §12 |
| `exploration_test.go` | 重写为新控制环的测试 |
| `web/src/**` | Exploration Tab 改为轮次视图 |
| `docs/architecture.md` | 更新探索控制章节 |

---

### 10.1 关键接口草图（仅示意，签名由实现者定）

```go
// Planner：Controller 的可替换实现
type Planner interface {
    Plan(ctx context.Context, req PlanRequest) (PlanOutput, error)  // req 带目标/事实账本/open items/上轮报告
}

// Explorer：一次一个 item 的执行者
type Explorer interface {
    Reset(ctx context.Context) error          // Pi new_session → 全新上下文
    Investigate(ctx context.Context, brief string) (Report, error)
}
```

---

## 11. 风险与必须先做的 spike

| # | 风险 | 缓解 / 验证方式 |
|---|---|---|
| S1 | Pi `new_session` 是否真的清空上下文（而非只换 session id） | **实现前先做 spike**：起一个 Pi session，`get_state` 读 `messageCount`，发 `new_session`，再读 `messageCount`，确认归零；并在真实 prompt 后确认模型不再记得旧内容 |
| S2 | 报告格式不合规导致轮次空转 | 简报里给严格 schema + 示例；校验失败只废单条 item；连续 2 轮全 failed 即停 |
| S3 | Controller 输出不合规（不是恰好 2 条 / JSON 坏） | 代码侧兜底：坏 JSON → 重试一次（附错误提示）→ 仍失败则本轮用确定性排序补齐，并在事件里标 `planner_degraded` |
| S4 | 3 个 Pi 进程的资源开销 | 启动前检查宿主机 CPU/内存；容器限额沿用现有值；`MaxRounds` 默认 6 控制总成本 |
| S5 | 两个 Explorer 撞同一条路 | item 全局唯一认领；简报里给出"已排除清单"；Controller 的 next_items 去重 |
| S6 | 事实账本膨胀导致简报超长 | 账本硬上限 + 摘要上限 8 条 + 每条截断 |
| S7 | 公开 API 破坏（删除 `ExplorationState` 等） | 需维护者明确批准（见 §14 待确认项 B）；`docs/architecture.md` 与 README 同步更新；`go vet`/编译即验证 |
| S8 | Controller 零工具是否被 Pi 接受（`pi --tools` 传空） | 若不接受，用只读 `read` 兜底（D12 已按"只读"定义权限，安全性不受影响） |

---

## 12. 留给实现者的部分（本文不规定）

- 内部包划分、文件命名、错误类型与错误文案。
- Controller / Explorer prompt 的具体措辞与 schema 排版（只要字段语义与 §6 一致）。
- `Planner` 接口的具体方法签名。
- 事实摘要"有界"的具体条数上限（除非与 §7.3 冲突）。
- 事件 payload 的字段命名细节。
- Web 视图的具体组件拆分。

---

## 14. 待维护者确认（builder check 提出的问题中，只有这两项需要人来定）

**A. TSec 提交权威**：Explorer 是否可以直接调用 `tsec-submit` 提交 flag？

- 选项 1（本文默认，推荐）：允许 Explorer 直接提交，harness 仍只认 backend completion 判定成功。
  沿用现状，零额外工程；提交动作留在 Explorer 的信任边界内（Explorer 已经持有 bash 与网络权限，
  收不收它其实收不回来）。
- 选项 2：Explorer 只在报告里给出 flag，由 harness 抽取并提交。
  提交权收归 harness，但需要 flag 抽取与校验逻辑（工程量大、易错、需要额外的 flag 格式知识）。

**B. 破坏性公开 API 删除是否批准**：删除 `ExplorationState`、`ReduceExploration`、`Frontier`、
`BuildExplorationContext`、`FormatExplorationContext`（D5 选择的"替换"路线）。

- 批准：仓库保持单一控制语义，代码量最小；SDK 使用者需要迁移。
- 不批准：需要保留 deprecated 兼容包装，等于两套概念并存。

---

## 13. 验收标准

1. `go test ./...`、`go test -race ./...`、`go vet ./...`、`go build ./cmd/harness` 全绿；
   前端 `lint`/`typecheck`/`test`/`build` 全绿。
2. 存在覆盖以下负面路径的测试：报告缺失 / 非法 JSON / `item_id` 不匹配 / `verdict` 非法 /
   `supports`/`refutes` 指向不存在的事实 / Controller 输出坏 JSON / Planner 降级 / 预算耗尽 /
   session 中止 / 报告被截断。
3. 存在证明 **Controller 输出无法产生成功判定** 的测试：仅 Verifier/backend 能置成功。
4. 存在证明 **每轮只注入 1 条 item、且 facts digest 有界** 的测试。
5. 存在证明 **同一 item 不会被两个 Explorer 同时认领** 的测试。
6. 存在证明 **冲突不静默覆盖**（双方保留 + contested + 最多一次复核 + 复核后裁决）的测试。
7. 真实 TSec 至少跑通 1 个挑战，且能从事件流还原出完整的轮次/事实/冲突链路。
