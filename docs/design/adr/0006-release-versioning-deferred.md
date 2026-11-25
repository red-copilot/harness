# ADR 0006: 模块路径暂不变，发布版本在 P9 前单独决定

- Status: Proposed (v5)
- Date: 2026-11-25
- Deciders: Red Harness maintainers
- Related: [migration.md](../migration.md)

## Context

"v5"可能被理解为三种不同的东西：

1. 产品/schema 代际（Red Harness v5、SQLite schema 5）；
2. Go 模块语义版本（`v5.0.0`）；
3. 发布标签（`.github/workflows/release.yml` 在 `v*` tag 上构建）。

Go 的模块语义版本要求 v2+ 的模块路径以 `/v5` 结尾，否则 `v5.0.0` 标签不会被工具链正确解析。重命名模块路径会让所有导入与适配器路径变化；使用 `+incompatible` 不适合有 `go.mod` 的模块。

用户明确选择"先不定发布版本"。因此 Phase 0 不决定模块路径或标签策略。

## Decision

1. 模块路径暂时保持 `github.com/red-copilot/harness`。
2. "v5"在设计与代码中表示**产品代际与 schema 5**，不表示 Go 模块版本。
3. 不在 Phase 0–P8 重命名模块路径或修改 release 标签策略。
4. 在 P9 之前单独做一次发布决策，并在 `release.yml` 与 `go.mod` 中落地；候选方案见下。
5. 根文档在 P9 前继续使用 v4/schema 8 的当前事实；`docs/design` 描述目标。

## Consequences

### Positive

- 内部导入路径稳定，避免在架构重构期间叠加模块重命名的噪声。
- 发布决策可以与代码切换解耦，按实际用户与分发方式选择。
- 不引入 `+incompatible` 或半正确的标签。

### Negative / costs

- 不能用当前路径发布 `v5.0.0`；需要 P9 前做出决定。
- 如果最终选择 `/v5`，仍会有一次全局导入路径变更。
- 文档必须明确"v5"指产品/schema，避免与语义版本混淆。

### Neutral

- 发布流程当前在 windows/amd64 上失败；无论版本策略如何，P9 前必须修复。
- 标签策略不影响内部架构，但影响用户升级路径。

## Options for P9

1. **模块路径改为 `/v5` 并发布 `v5.x`**：语义正确；一次性改导入。
2. **保留模块路径，继续发布 `v4.x`，把 v5 作为产品名**：避免模块重命名，但版本号与产品名不一致。
3. **暂不发布标签，只发布二进制/容器**：适合以 CLI 为主的用户；需要明确 SDK 的获取方式。
4. **新建仓库/模块路径**：隔离最强，但迁移与发现成本最高。

## References

- `go.mod`（当前模块路径）、`.github/workflows/release.yml:41`（windows/amd64）。
- [migration.md](../migration.md) P9。
