# ADR 0003: 公开 API 为 core + engine，删除根 facade 与别名层

- Status: Proposed (v5)
- Date: 2026-11-25
- Deciders: Red Harness maintainers
- Related: [architecture.md](../architecture.md), [ports.md](../ports.md)

## Context

v4 中同一个领域符号最多有三个 import 路径：

1. `core.X`（定义）；
2. `engine.X`（`engine/core_aliases.go`，163 个 `type X = core.X`）；
3. `harness.X`（根 `harness.go`，约 194 行再导出）。

后果：

- 每加一个类型/常量要改最多三个文件；
- 根 facade 无测试，且容易承载状态机/I/O；
- 适配器（如 `verify/command.go`）导入根包，导致适配器依赖 SDK 表面而不只是契约；
- 文档与代码对"公开 API 是什么"没有唯一答案。

## Decision

v5 的公开 API 是 **`core` + `engine`**：

- 删除根 `harness.go`；
- 删除 `engine/core_aliases.go`；
- 适配器包保留现有路径：`pi/`、`sandbox/docker/`、`verify/`、`store/sqlite/`；
- 新增 `runtime/` 作为唯一组合根，它不是 SDK 领域表面；
- 禁止生产代码中的跨包类型别名；
- 适配器直接导入 `core`/`engine`。

## Consequences

### Positive

- 每个符号只有一个名称、一个 import 路径。
- 适配器依赖契约而非 facade；依赖方向可由 arch 测试强制。
- 配置/装配逻辑集中到 `runtime`，CLI 只做操作面。
- 删除无测试的 facade，减少隐式 API。

### Negative / costs

- 对所有使用根 `harness` 包的用户是破坏性变更。
- 需要一次性修改大量导入（P7）。
- 适配器路径未统一到 `adapters/`，目录语义仍靠约定与 arch 测试；这是为降低机械改动而做的取舍。

### Neutral

- `runtime` 的 API 仍需稳定，但它不是领域契约，可以独立演进。
- 如果未来需要更清晰的目录，可另开 ADR 把适配器移到 `adapters/`。

## Alternatives considered

1. **保留薄根 facade 作为唯一 SDK 入口**：可以改善用户体验，但仍需维护一层重导出，且与"每个符号一个名称"冲突。拒绝。
2. **保留别名层**：继续三处命名与三处修改。拒绝。
3. **统一移到 `adapters/`**：目录最清晰，但导入路径全改、PR 机械噪声大；与本次架构目标无直接关系。暂不采用，保留为未来选项。

## References

- v4 证据：`harness.go`、`engine/core_aliases.go`、`verify/command.go:14`（适配器导入根包）、`import_boundary_test.go`（未测试根 facade）。
- v5 目标：[architecture.md](../architecture.md) 第 3–5 节、[ports.md](../ports.md)。
