# ADR 0005: 分阶段直接进 main，过渡层在 P9 删除

- Status: Proposed (v5)
- Date: 2026-11-25
- Deciders: Red Harness maintainers
- Related: [migration.md](../migration.md)

## Context

v5 是破坏性重构：新公开 API、新状态机、新持久化、仅 verifier 权威。仓库的 `CLAUDE.md`/`AGENTS.md` 要求：

- 永远从 `main` 切短生命周期分支；
- 通过门禁后 squash 合并；
- 不长期悬挂未合并的已验证工作。

用户明确选择"分阶段直接进 main"，而不是长期 `release/v5` 分支或单个大 PR。这意味着需要一个在 `main` 上逐步演进、每个合并点都绿的方案。

## Decision

1. 交付分为 P0–P9，每个阶段是短生命周期分支 → PR → 门禁 → squash 合并。
2. 只保留一个 kernel。P4 用一个**临时 v4 存储桥**把 v5 记录/状态映射到 v8 表，使 `main` 在过渡期继续运行 v8 默认库。
3. 新 v5 store 在 P3 以 additive 方式引入，P4 起与 v4 桥共同通过同一套 `engine/porttest`。
4. 兼容层（v4 桥、旧 core 类型、`harness/v4` 配置映射、v8 备份工具）登记在 [migration.md](../migration.md) 的清单中；P9 必须删除。
5. P9 翻转默认到 v5、执行 cutover、删除 v4 路径，并更新根文档。
6. 每个 PR 描述必须列出"新增/删除的过渡层"；P9 的退出条件包含 grep 检查。

## Consequences

### Positive

- `main` 始终可构建、可测试；没有长期分支腐化。
- 每个阶段可独立评审、回滚。
- 兼容层有明确生命周期，避免永久双实现。

### Negative / costs

- P4 的 v4 存储桥是额外工作量，且有语义偏差风险；必须用同一套 porttest 与端到端对照验证。
- 过渡期存在旧类型/旧配置映射，代码审查需要额外关注。
- 交付日历比单一大 PR 更长。

### Neutral

- 阶段边界按"可验证的行为变化"划分，而不是按文件数量。
- 如果某阶段无法保持 `main` 绿，必须拆分，而不是延长分支寿命。

## Alternatives considered

1. **长期 `release/v5` 分支**：隔离干净，但违背仓库工作流，最终合并风险集中。用户已拒绝。
2. **单个大 PR**：历史简单，但评审面巨大、无法分阶段验证。用户已拒绝。
3. **并行 v5 包树 + 运行时切换**：两个 kernel 长期并存，腐化风险更高。拒绝。
4. **先修 v4 再重构**：会做将被删除的工作；只保留对 v5 有复用价值的 P1 准备。

## References

- [migration.md](../migration.md) 第 2–3 节（阶段表、兼容层清单）。
- 仓库工作流：`CLAUDE.md` 的 "Git workflow" 与 `AGENTS.md` 的 "Automated Git Workflow"。
