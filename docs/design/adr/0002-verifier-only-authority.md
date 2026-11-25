# ADR 0002: 仅保留 Verifier 权威

- Status: Proposed (v5)
- Date: 2026-11-25
- Deciders: Red Harness maintainers
- Related: [verification-redaction.md](../verification-redaction.md)

## Context

v4 文档宣称成功可以来自 `Verifier.Verify` **或** backend 的 terminal `CompletionSource.Observe`。实际上：

- 没有任何生产 `CompletionSource`/`Backend` 实现；
- CLI 从不设置 `RunSpec.Backend`，也无配置字段；
- backend watcher、authority 事件与 `VerdictAuthority` 只被测试使用；
- `README.md:19` 仍把它描述为一等权威。

同时，v4 的 `arbitrate` 在检查 `stagnated` 之前先检查 verifier，导致 `stagnated` 结果几乎不可达：一个停滞的 Run 仍可能被 verifier 判为成功。这与"停滞是非成功"的文档语义冲突。

## Decision

v5 **只保留 Verifier 权威**：

- 删除 `Completion`、`CompletionSource`、`Backend`、backend watcher、`completionVerdict`、authority 事件与 `VerdictAuthority*`；
- `Verifier` 在 `engine.New`/`Resume` 时必填；
- 成功当且仅当 `goal_candidate` 触发的验证返回 `Success=true`；
- 停滞、取消、超时、预算、control error 一律不调用 verifier，且不能被 verifier 覆盖；
- `Verdict = {Success, Score, Message, Evidence}`；verifier 身份仅用于审计；
- 文档与示例同步改为单权威。

## Consequences

### Positive

- API 与文档一致；删除不可达表面与测试专用路径。
- `stagnated`/`cancelled`/`timed_out` 成为真实可达的终态，语义可测。
- 成功链更短、更容易证明（I2/I3）。

### Negative / costs

- 对假设存在 backend 权威的库用户是破坏性变更。
- 依赖"外部系统完成即成功"的部署需要改用 verifier 适配器（例如轮询外部系统并返回 Verdict）。
- 配置中旧的 backend 键会被拒绝，需要迁移提示。

### Neutral

- verifier 的调用时机更严格（仅 goal_candidate），可能改变某些 Run 的 outcome；这是有意的语义修正。
- 审计仍保留成功 verdict，即使 cleanup 失败把 Run 降级为 failed。

## Alternatives considered

1. **实现 backend CompletionSource 并打通 CLI**：保留双权威，但需要真实的完成源、轮询语义、失败策略与跨系统契约；当前没有需求方。拒绝。
2. **保持双权威但仅库内可用**：继续维护不可达表面与文档漂移，违背第一性原理。拒绝。
3. **只删文档、不改代码**：无法修复 `stagnated` 不可达与成功判定歧义。拒绝。

## References

- v4 证据：`engine/ports.go:38-47`、`engine/run_controller.go`（watcher/arbitrate）、`README.md:19`、`core/types_evidence.go`（`VerdictAuthority`）。
- v5 目标：[verification-redaction.md](../verification-redaction.md)、[state-machine.md](../state-machine.md)。
