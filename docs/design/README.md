# Red Harness v5 设计文档

> **状态：v5 目标设计（提案）。** 当前仓库代码仍是 Red Harness v4、SQLite schema 8、RunDescriptor schema 4。P9 切换完成前，本目录描述的是目标，不是现状；根目录 `README.md`、`AGENTS.md`、`CLAUDE.md` 仍以 v4/v8 为准。

## 为什么有这个目录

v4 的门禁是绿的，但若干关键性质只由文档承诺，未被结构保证：resume 在 CLI 路径上不可达、artifact 写入可绕过脱敏、phase 更新非原子、持久化存在三套可漂移的表示、双权威没有生产实现。v5 的目标不是增加更多文档约束，而是让不变量在类型、端口、SQL 约束和测试中**无法被绕过**。

## 阅读顺序

| 顺序 | 文档 | 回答的问题 |
|---|---|---|
| 1 | [invariants.md](invariants.md) | 系统必须永远为真的性质是什么？ |
| 2 | [architecture.md](architecture.md) | 目标组件、包边界和依赖方向是什么？ |
| 3 | [state-machine.md](state-machine.md) | 一个 Run 如何合法地流转与终结？ |
| 4 | [data-model.md](data-model.md) | 什么是唯一真相，schema v5 如何保证它？ |
| 5 | [ports.md](ports.md) | kernel 与外部世界的窄契约是什么？ |
| 6 | [recovery.md](recovery.md) | 谁拥有一个 Run，崩溃后如何恢复？ |
| 7 | [verification-redaction.md](verification-redaction.md) | 什么算成功，什么绝不能落盘？ |
| 8 | [migration.md](migration.md) | 如何分阶段直接进 main 并完成切换？ |

## 决策记录

| ADR | 决策 |
|---|---|
| [0001](adr/0001-event-sourced-journal.md) | 采用 per-run 事件日志作为唯一真相 |
| [0002](adr/0002-verifier-only-authority.md) | 仅保留 Verifier 权威 |
| [0003](adr/0003-public-api-core-engine.md) | 公开 API 为 `core` + `engine`，删除根 facade 与别名层 |
| [0004](adr/0004-lease-and-ownership.md) | 写者 lease 下沉到存储层，中断使用 CAS |
| [0005](adr/0005-incremental-delivery-on-main.md) | 分阶段直接进 main，过渡层在 P9 删除 |
| [0006](adr/0006-release-versioning-deferred.md) | 模块路径暂不变，发布版本在 P9 前单独决定 |
| [0007](adr/0007-schema-v5-cutover.md) | schema v5 采用备份→验证→OpenFresh 单向切换 |

## 当前与目标对照

| 维度 | 当前 v4 | 目标 v5 |
|---|---|---|
| 权威 | Verifier 或 backend completion | 仅 `Verifier.Verify` |
| 持久化真相 | 实体表 + JSON payload + `board_commits` 快照 | per-run journal；其余为可重建投影 |
| 审计链 | 全局、跨 Run | per-run，genesis = manifest digest |
| 端口 | 21 方法 `Store` 并集 | 窄 `Journal`/`Reader`/`Recovery`/`ArtifactWriter` 等 |
| 脱敏 | `redactingStore` 装饰器，artifact `Put` 未包裹 | `Sealed*` 类型，单一写边界 |
| Resume | 不可达、信任调用方 spec | manifest + journal 回放，lease 互斥 |
| Phase 写入 | Read + Update + Event 三步 | 单次 journal append + head CAS |
| 公开 API | 根 facade + `engine/core_aliases.go` + 三处命名 | `core` + `engine`，无别名 |
| 配置 | `harness/v4`，嵌套 `any` 宽松 | `harness/v5`，逐层严格 |

## 评审检查点

Phase 0 结束时需要对以下事项明确签核：

1. `invariants.md` 的 I1–I10 是否完整、是否每项都有结构性执行点。
2. `state-machine.md` 的阶段/结果/转移矩阵是否覆盖所有失败路径。
3. `data-model.md` 的 schema v5 与 finalize 单向顺序是否正确。
4. 仅 Verifier 权威的仲裁顺序与 `goal_candidate` 触发条件是否符合预期。
5. `ports.md` 的窄端口是否足够实现现有能力。
6. `migration.md` 的 P1–P9 顺序、兼容层清单与 P9 删除点是否可执行。

任何一项未签核，不进入 P1。
