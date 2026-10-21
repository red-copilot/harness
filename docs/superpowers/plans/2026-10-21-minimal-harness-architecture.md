# Minimal Harness Architecture Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce the Harness core to run/session/audit/verdict orchestration and move binary-tree exploration behind an injected workflow boundary without changing CLI/HTTP/Web behavior.

**Architecture:** `Runner` owns run lifecycle, backend observation, budgets, session cleanup, persistence, and verdict authority. A narrow `Workflow` receives a `WorkflowRuntime` that can only open sessions, emit audited events, and read bounded run context. The existing v2 exploration engine moves to a separate `exploration` package and is configured by adapters.

**Tech Stack:** Go 1.24, Pi RPC, SQLite (`modernc.org/sqlite`), React/Vite/TypeScript, Vitest.

**Spec:** `docs/superpowers/specs/2026-10-21-minimal-harness-architecture-design.md`

## Global Constraints

- Only a `Verifier` or backend completion may produce a successful verdict.
- `Runner` is the single owner of lifecycle, budgets, cancellation, verdicts, and cleanup.
- Core `harness` must not import `internal/cli`, `server`, `pi`, `tsec`, Docker, or exploration control code.
- A workflow may open sessions and emit events; it may not access Store, Backend, Verifier, or finalize a Run.
- Every model-facing digest remains bounded.
- Every persisted event/log is redacted, and a persistence failure terminates the run.
- Session IDs are unique within a run and `Config.MaxSessions` is enforced in the session runtime.
- SQLite remains canonical and append-only; no migration deletes audit rows.
- Pi completion remains `agent_settled`; `agent_end` is not completion.
- Docker containers retain read-only root, `no-new-privileges`, capability dropping, resource limits, and no Docker socket.
- Existing CLI YAML `exploration:` blocks and HTTP event shapes remain valid.
- Direct operator steering remains available through `Run.Steer`.
- Tests are written first and watched fail for the expected missing behavior.
- Conventional Commits with a scope; run the full verification matrix before commit/push.

## Review Focus

- A workflow returns normally while a session or log producer is still alive: finalization must wait, abort, close, and record every opened session.
- An event or log write fails after the workflow observed it: the run must fail rather than return a verdict.
- A backend completion races with workflow completion: exactly one authoritative result must win and workflow work must be cancelled.
- Two workflows or goroutines open the same session ID: the second open must fail without replacing the first lease.
- An exploration policy reaches the persisted run spec without the runtime workflow object and without leaking secrets.

---

### Task 1: Add the Core Workflow Runtime and Direct Workflow

**Files:**
- Modify: `types.go`
- Modify: `runner.go`
- Create: `workflow.go`
- Test: `workflow_test.go`
- Modify: `runner_test.go`

**Interfaces:**
- Produces: `type Workflow interface { Execute(context.Context, WorkflowRuntime) (WorkflowResult, error) }`
- Produces: `type WorkflowRuntime interface` with `RunID`, `Task`, `Target`, `Agents`, `Budget`, `WorkspaceRoot`, `Progress`, `OpenSession`, `Emit`, and `Redact` methods exactly as defined in the spec.
- Produces: `type SessionLease struct { ID string; Agent AgentSpec; WorkspaceDir string; Session AgentSession }`
- Produces: `func (r *Runner) Start(context.Context, RunSpec) (*Run, error)` and `func (r *Runner) Run(context.Context, RunSpec) (Result, error)` unchanged.
- Produces: `RunSpec.Workflow Workflow` with `json:"-" yaml:"-"`, and `RunSpec.WorkflowConfig *WorkflowConfig` with a serializable name/raw config.

- [ ] **Step 1: Write the custom-workflow test**

Add `TestRunnerExecutesInjectedWorkflow` in `workflow_test.go` using `package harness_test`. Define a workflow that records that `RunID`, `Task.ID`, `Target.ID`, and `Budget.WallTime` are visible, opens the declared agent once, emits one `workflow.observed` event, and returns `WorkflowResult{}`. Use a fake session that succeeds and a verifier returning success. Assert the run succeeds and one session result with ID `agent-1` exists.

- [ ] **Step 2: Write the session-boundary tests**

Add `TestWorkflowRuntimeRejectsDuplicateSessionID` with two concurrent `OpenSession` calls for `agent-1`; assert one succeeds and one returns an error containing `already`. Add `TestWorkflowRuntimeEnforcesMaxSessions` with `Config.MaxSessions=1`; assert the second distinct open fails and no second session is registered.

- [ ] **Step 3: Write the event fail-closed test**

Add `TestWorkflowEventPersistenceFailureFailsRun` using a store whose `AppendEvent` fails on `workflow.observed`. Assert `Runner.Run` returns `StatusFailed`, a non-nil error, and never returns a successful verdict.

- [ ] **Step 4: Run the new tests and confirm RED**

Run: `go test ./... -run 'TestRunnerExecutesInjectedWorkflow|TestWorkflowRuntimeRejectsDuplicateSessionID|TestWorkflowRuntimeEnforcesMaxSessions|TestWorkflowEventPersistenceFailureFailsRun'`
Expected: FAIL to compile because the workflow/runtime contracts do not exist.

- [ ] **Step 5: Implement the core contracts and direct workflow**

Add the spec-defined interfaces and config types. Implement an unexported `directWorkflow` that opens every declared agent, emits session events through `WorkflowRuntime.Emit`, calls `Prompt(buildPrompt(...))`, waits for each session, and joins failures. Keep the existing `Run.Steer` behavior by registering successful leases with `Run.addSession`.

- [ ] **Step 6: Replace the old runner execution loop**

Implement a run-scoped execution runtime that owns session creation, duplicate/concurrency checks, event emission, redaction, budget checks, log pumps, and deterministic session finalization. Select `spec.Workflow` or `directWorkflow`, run it with `run.ctx`, race backend completion against workflow completion, then finalize sessions before verifier execution. Preserve existing result/status/event names.

- [ ] **Step 7: Run focused and full Go tests**

Run: `go test ./... -run 'TestRunner|TestWorkflow'`
Expected: PASS for direct, custom-workflow, budget, cancellation, backend, and steering tests.

- [ ] **Step 8: Commit**

```bash
git add types.go runner.go workflow.go workflow_test.go runner_test.go
git commit -m "refactor(runner): add minimal workflow runtime"
```

---

### Task 2: Extract the Exploration Package and Remove v1 Control Code

**Files:**
- Create: `exploration/model.go`
- Create: `exploration/engine.go`
- Create: `exploration/session.go`
- Create: `exploration/turn.go`
- Create: `exploration/workflow.go`
- Create: `exploration/configure.go`
- Test: `exploration/engine_test.go`
- Test: `exploration/session_test.go`
- Test: `exploration/workflow_test.go`
- Delete: `exploration_controller.go`, `exploration_controller_test.go`
- Delete: `explorer.go`, `explorer_test.go`, `planner.go`, `planner_test.go`
- Delete: `frontier.go`, `facts.go`, `facts_test.go`, `state.go`, `exploration_test.go`
- Delete: root `exploration_v2.go`, `exploration_tree.go`, `exploration_tree_session.go`, `exploration_runner.go` and their root tests after moving coverage

**Interfaces:**
- Consumes: `harness.Workflow`, `harness.WorkflowRuntime`, `harness.SessionLease`, `harness.AgentSession`, `harness.Event`.
- Produces: `exploration.Policy`, `exploration.DefaultPolicy()`, `exploration.NormalizePolicy(Policy) Policy`.
- Produces: `exploration.NewWorkflow(Policy, Store) harness.Workflow`.
- Produces: `exploration.Configure(*harness.RunSpec, Policy, harness.Store) error`, setting `Workflow` and `WorkflowConfig{Name:"exploration"}`.
- Produces: `exploration.Branch`, `Attempt`, `Observation`, `Knowledge`, `BranchStore`, and `KnowledgeStore` for SQLite/server adapters.
- Produces: `type EventSink func(harness.AgentEvent) error`.

- [ ] **Step 1: Move the existing v2 tests first**

Copy the current v2 tree/session tests into `exploration/` and change the package to `exploration`. Rewrite test references from unqualified harness types to `harness.` where required. Keep tests for policy defaults, branch validation, knowledge validation, binary child caps, review branches, planner/executor reset, and stagnation.

- [ ] **Step 2: Add workflow-level RED tests**

Add `TestWorkflowRunsPlannerAndExecutorThroughRuntime`, `TestWorkflowReturnsStagnatedResult`, and `TestWorkflowStopsWhenControlEventFails`. The first asserts session IDs are `planner` and `executor-1`; the second asserts a bounded no-progress fixture returns `WorkflowResult{Stagnated:true}`; the third asserts the runtime event error is returned unchanged.

- [ ] **Step 3: Run the exploration tests and confirm RED**

Run: `go test ./exploration/...`
Expected: FAIL to compile because the package does not exist.

- [ ] **Step 4: Move the v2 model and engine**

Move only the binary-tree model, validation, policy, engine, and prompt/session adapters. Change `BranchEngineConfig.Emit` to `func(BranchEvent) error` and propagate every emit and store error through `Run`; do not preserve fire-and-forget saves.

- [ ] **Step 5: Implement the exploration workflow**

Resolve role agents from `WorkflowRuntime.Agents()`, open planner/executor sessions lazily, build adapters using a sink that calls `WorkflowRuntime.Emit`, run `BranchEngine`, and return stagnation. Wrap branch/knowledge stores so string fields are redacted through `WorkflowRuntime.Redact` before writes.

- [ ] **Step 6: Delete the v1 control implementation**

Delete Controller, FactLedger, Frontier, Explorer, Planner, legacy state reduction, and their tests. Do not replace them with aliases. Keep only the direct-steering behavior owned by `Run.Steer`.

- [ ] **Step 7: Run package and full Go tests**

Run: `go test ./exploration/... && go test ./...`
Expected: PASS; no root package symbol or file references the deleted v1 implementation.

- [ ] **Step 8: Commit**

```bash
git add exploration exploration_controller.go exploration_controller_test.go explorer.go explorer_test.go planner.go planner_test.go frontier.go facts.go facts_test.go state.go exploration_test.go exploration_v2.go exploration_tree.go exploration_tree_session.go exploration_runner.go exploration_v2_test.go exploration_tree_session_test.go exploration_runner_test.go
git commit -m "refactor(exploration): isolate optional workflow"
```

---

### Task 3: Rewire CLI, Server, SQLite, and Request Contracts

**Files:**
- Modify: `internal/cli/config.go`
- Modify: `internal/cli/cli.go`
- Modify: `server/server.go`
- Modify: `store/sqlite/exploration_v2.go`
- Test: `internal/cli/config_test.go`
- Test: `server/server_test.go`
- Test: `store/sqlite/exploration_v2_test.go`

**Interfaces:**
- Consumes: `exploration.Configure`, `exploration.DefaultPolicy`, `exploration.Policy`.
- Produces: server `RunRequest.Exploration *exploration.Policy`.
- Preserves: YAML key `exploration:` and all existing CLI flags.
- Preserves: `/v1/runs/{id}/branches`, `/v1/tasks/{id}/knowledge`, and exploration event payloads.

- [ ] **Step 1: Write adapter RED tests**

In `internal/cli/config_test.go`, assert an explicit exploration block and the implicit single-agent default both produce `WorkflowConfig.Name == "exploration"` after configuration with a fake store. In `server/server_test.go`, assert a request with top-level `exploration` reaches the builder and the resulting run spec has an exploration workflow config. In `store/sqlite/exploration_v2_test.go`, change types to `exploration.*` and assert persistence round-trips.

- [ ] **Step 2: Run adapter tests and confirm RED**

Run: `go test ./internal/cli ./server ./store/sqlite`
Expected: FAIL to compile because adapters still use root exploration types and `RunSpec.Exploration`.

- [ ] **Step 3: Rewire CLI configuration**

Keep `FileConfig.Exploration *exploration.Policy`. Split `ToRunSpec` from workflow attachment: after the CLI opens the store, call `exploration.Configure` for explicit policy; for the existing single-agent default, call it with `exploration.DefaultPolicy()`. Ensure the stored spec contains only `WorkflowConfig`, not the runtime workflow object.

- [ ] **Step 4: Rewire HTTP and SQLite**

Add top-level `Exploration` to `server.RunRequest`, pass it to `RunRequest.Run` workflow configuration in the CLI builder, and import exploration types in branch/knowledge readers. Update SQLite method signatures and tests to use `exploration` package types. Keep JSON response fields unchanged.

- [ ] **Step 5: Run adapter and full Go tests**

Run: `go test ./internal/cli ./server ./store/sqlite && go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/config.go internal/cli/cli.go internal/cli/config_test.go server/server.go server/server_test.go store/sqlite/exploration_v2.go store/sqlite/exploration_v2_test.go
git commit -m "refactor(adapters): wire optional exploration workflow"
```

---

### Task 4: Align Documentation and Verify the Whole Change

**Files:**
- Modify: `README.md`
- Modify: `docs/architecture.md`
- Modify: `docs/superpowers/specs/2026-10-21-minimal-harness-architecture-design.md` only if implementation rulings require it

**Interfaces:**
- Documents: five core responsibilities, `Workflow` injection, optional exploration package, unchanged CLI/API behavior, and removed v1 control semantics.

- [ ] **Step 1: Update architecture documentation**

Replace the mixed v1/v2 exploration descriptions with one core execution model and one optional exploration workflow. State that the core owns verdicts and audit, while the exploration package owns branch scheduling and knowledge promotion.

- [ ] **Step 2: Update the README integration example**

Show `exploration.Configure(&spec, exploration.DefaultPolicy(), store)` for exploration-enabled library callers and state that omitting a workflow selects the direct session workflow. Keep the current CLI YAML example valid.

- [ ] **Step 3: Check for stale symbols**

Run: `rg -n 'FactLedger|Frontier|NewExplorationState|SessionExplorer|SessionPlanner|RunSpec\\.Exploration|harness\\.Branch|harness\\.Knowledge' --glob '*.go' --glob '*.md' .`
Expected: no production references; documentation may name removed concepts only in a migration note.

- [ ] **Step 4: Run the full verification matrix**

Run each command and require exit 0:

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/harness
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
git diff --check
```

- [ ] **Step 5: Commit**

```bash
git add README.md docs/architecture.md docs/superpowers/specs/2026-10-21-minimal-harness-architecture-design.md
git commit -m "docs(architecture): describe minimal harness core"
```

## Plan Self-Review

- Spec coverage: runtime boundary (Task 1), optional exploration and v1 deletion (Task 2), adapters and compatibility (Task 3), docs and acceptance commands (Task 4).
- Failure coverage: duplicate sessions, max sessions, event persistence, backend/workflow race, cleanup, and adapter request wiring each have a test.
- Type consistency: `Workflow`, `WorkflowRuntime`, `SessionLease`, `WorkflowResult`, `exploration.Configure`, `DefaultPolicy`, and `NewWorkflow` are introduced before consumption.
- Intentional compatibility break: root-package exploration control symbols are not re-exported from `harness`; v1alpha1 API users must import `exploration`.
- No migrations are added or changed; existing audit schema remains canonical.
