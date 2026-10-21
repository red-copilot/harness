# Hypothesis Frontier Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace long-lived, context-dependent exploration with a deterministic blackboard controller that dispatches one fresh-context hypothesis frontier at a time to an executor and persists durable facts and authoritative progress.

**Architecture:** The Planner proposes bounded frontiers from a blackboard snapshot; the Controller validates, deduplicates, leases, dispatches, and merges; each Executor owns exactly one frontier in a fresh Pi session; SQLite remains the canonical append-only audit/event store. Agent reports and facts never establish success; only a Verifier or backend completion does.

**Tech Stack:** Go 1.24, Pi LF-delimited JSONL RPC, Docker sandbox, SQLite (`modernc.org/sqlite`), React/Vite observability UI.

**Spec:** `docs/superpowers/specs/2026-10-21-exploration-expansion-design.md`

## Global Constraints

- Success is produced only by `Verifier` or backend completion.
- The Planner proposes; deterministic Go code validates and dispatches.
- Each Executor handles exactly one `FrontierItem` per turn in a fresh context.
- Canonical memory is `Goal progress + FactLedger + FrontierItem history`, never the chat transcript.
- SQLite is the canonical audit store; raw logs stay out of Planner/Executor summaries.
- Same frontier fingerprint cannot be dispatched again unless an explicit dependency changed.
- `agent_settled` is completion of a Pi prompt; `agent_end` is not.
- Reports with malformed JSON, mismatched item IDs, unauthorized targets, or missing evidence fail closed.
- Known secrets are redacted before persistence and excluded from Planner/Executor summaries.
- Docker containers receive no Docker socket and retain read-only root, dropped capabilities, and resource limits.
- Default exploration concurrency is two Executors, each handling exactly one Frontier per turn.
- Conventional Commits with a scope; after verification, commit task files and push the current branch.

## Review Focus

1. A Planner returns malformed JSON or duplicate frontiers: the Controller degrades to target seeds, records diagnostics, and never declares success.
2. An Executor returns a report for another frontier: that attempt fails and contributes no facts.
3. A failed or refuted path is proposed again: fingerprint deduplication blocks it before tool execution.
4. Context compaction or session reset occurs: Planner and Executor reconstruct from the blackboard, not transcript memory.
5. Budget or wall time expires during planning or execution: sessions stop and the run produces no successful verdict.

---

### Task 1: Frontier, Report, and Planner Contracts

**Files:**
- Modify: `types.go`
- Modify: `frontier.go`
- Create: `explorer.go`
- Create: `planner.go`
- Test: `explorer_test.go` (already staged as a failing test)
- Test: `planner_test.go`

**Interfaces:**
- Consumes: `FactLedger`, `Fact`, `ExplorerClaim`.
- Produces: `FrontierItem`, `ExplorerReport`, `ExplorerProposal`, `PlanRequest`, `PlanOutput`, `Planner`, `ValidatePlan`, `SeedItems`, `AuthorizeTarget`, `DigestFacts`, `BuildExplorerBrief`, `FrontierFingerprint`.

- [x] **Step 1: Run the existing Explorer report tests and confirm they fail on the missing frontier/report contract.**
- [x] **Step 2: Add failing Planner tests for malformed output, unauthorized targets, duplicate fingerprints, anchor filtering, and deterministic seed frontiers.**
- [x] **Step 3: Implement the new fields and pure validation/planning helpers without changing Runner behavior.**
- [x] **Step 4: Run `go test ./... -run "TestParseExplorerReport|TestBuildExplorerBrief|TestDigestFacts|TestAuthorizeTarget|TestPlanner|TestValidatePlan|TestSeedItems"`; expect PASS.**
- [x] **Step 5: Commit `explorer.go`, `planner.go`, `frontier.go`, `types.go`, and tests with `feat(exploration): add frontier planning contracts`.**

### Task 2: Deterministic Frontier Controller

**Files:**
- Create: `exploration_controller.go`
- Create: `exploration_controller_test.go`

**Interfaces:**
- Consumes: `Planner`, `Explorer`, `FactLedger`, `ParseExplorerReport`, `ValidatePlan`, `seedFrontierItems`.
- Produces: `Controller`, `NewController`, `Controller.Run`, `Controller.Facts`, `Controller.Items`, `ControllerEvent`.

- [x] **Step 1: Write failing tests proving one frontier per Executor, fresh reset per attempt, exact fingerprint deduplication, mismatched report failure, bad Planner degradation, no success from reports, and bounded rounds.**
- [x] **Step 2: Implement the sequential/parallel-neutral round loop with code-owned validation and merging.**
- [x] **Step 3: Run `go test ./... -run TestController`; expect PASS.**
- [x] **Step 4: Commit with `feat(exploration): add hypothesis frontier controller`.**

### Task 3: Fresh-Context Pi Adapters

**Files:**
- Modify: `pi/session.go`
- Create: `pi/session_reset_test.go`
- Create: `session_agent.go`
- Create: `session_agent_test.go`

**Interfaces:**
- Consumes: `AgentSession`, `FrontierItem`, `ExplorerReport`, `PlanRequest`.
- Produces: `ResettableSession`, `RunTurn`, `SessionPlanner`, `SessionExplorer`, and Pi `Reset(context.Context) error`.

- [x] **Step 1: Write failing tests for Pi `new_session`, assistant-text extraction, report file parsing, and mismatched report IDs.**
- [x] **Step 2: Implement reset, structured turn collection, and Planner/Executor adapters.**
- [x] **Step 3: Run `go test ./pi ./... -run "TestReset|TestRunTurn|TestSession"`; expect PASS.**
- [x] **Step 4: Commit with `feat(pi): add fresh-context exploration adapters`.**

### Task 4: Runner and CLI Integration

**Files:**
- Modify: `runner.go`
- Modify: `internal/cli/config.go`
- Modify: `internal/cli/cli.go`
- Modify: `types.go`
- Modify: `runner_test.go`
- Create: `exploration_runner_test.go`

**Interfaces:**
- Consumes: `Controller`, `SessionPlanner`, `SessionExplorer`, `ExplorationSpec`.
- Produces: `RunSpec.Exploration`, role expansion from one AgentSpec, exploration event persistence, and backend-progress injection into Planner snapshots.

- [x] **Step 1: Write failing Runner tests for role expansion, one-frontier dispatch, event persistence, backend completion preemption, and cancellation.**
- [x] **Step 2: Add an exploration execution path while preserving the existing custom multi-session path.**
- [x] **Step 3: Make CLI configs enable exploration by default for single-agent runs and reject invalid role references.**
- [x] **Step 4: Run `go test ./...`; expect PASS.**
- [x] **Step 5: Commit with `feat(runner): dispatch stateless hypothesis frontiers`.**

### Task 5: Authoritative Multi-Flag Progress

**Files:**
- Modify: `types.go`
- Modify: `tsec/client.go`
- Modify: `tsec/client_test.go`
- Modify: `runner.go`

**Interfaces:**
- Consumes: `Completion`.
- Produces: generic `Completion.Progress`, `Completion.Total`, periodic backend progress events, and Planner-visible progress snapshots.

- [x] **Step 1: Write failing TSec and Runner tests for `2/6` progress without completion.**
- [x] **Step 2: Populate progress from TSec observations and cache the latest observation for Planner snapshots.**
- [x] **Step 3: Run `go test ./... -run "TestBackend|TestRunner.*Progress"`; expect PASS.**
- [x] **Step 4: Commit with `feat(tsec): expose authoritative flag progress`.**

### Task 6: Observability and Documentation

**Files:**
- Modify: `web/src/utils/exploration.ts`
- Modify: `web/src/utils/exploration.test.ts`
- Modify: `web/src/i18n/messages.ts`
- Modify: `docs/architecture.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: `exploration.*` events emitted by Runner.
- Produces: readable frontier/attempt/fact timeline and documented configuration/defaults.

- [x] **Step 1: Write failing frontend tests for the new exploration event mapping.**
- [x] **Step 2: Implement the minimal timeline mapping and documentation updates.**
- [x] **Step 3: Run `npm --prefix web run test`, `npm --prefix web run typecheck`, `npm --prefix web run lint`, `npm --prefix web run build`, `go test ./...`, `go test -race ./...`, `go vet ./...`, and `go build ./cmd/harness`.**
- [x] **Step 4: Commit with `docs(exploration): document hypothesis frontier engine` and push.**

### Task 7: Explicit Initialization Before Executor Dispatch

**Files:**
- Modify: `exploration_controller.go`
- Modify: `exploration_runner.go`
- Modify: `exploration_controller_test.go`
- Modify: `exploration_runner_test.go`
- Modify: `web/src/utils/exploration.ts`
- Modify: `web/src/utils/exploration.test.ts`
- Modify: `web/src/components/ExplorationTimeline.tsx`
- Modify: `web/src/i18n/messages.ts`

**Interfaces:**
- Consumes: `ControllerConfig`, `ControllerEvent`, `FrontierItem`, `Explorer`, `Completion`.
- Produces: `ControllerConfig.AcquireExplorers`, `exploration.initialized`, `exploration.frontiers.created`, `exploration.batch.ready`, `exploration.executor.started`, full item payloads, and `HypothesisView.phase/objective/targets/openFrontiers`.

- [x] **Step 1: Write failing Controller tests for initialization event ordering, complete frontier publication, and lazy executor acquisition.**
- [x] **Step 2: Implement explicit `initialized -> planning -> ready -> dispatching -> executing` events; publish full accepted frontiers and batch items before assignment.**
- [x] **Step 3: Make Runner create executor sessions lazily through `AcquireExplorers`; synthesize non-started executor audit rows when planning yields no work.**
- [x] **Step 4: Write failing frontend tests for restoring phase, goal, targets, open frontiers and current assignment before any report exists.**
- [x] **Step 5: Implement the initialization view and run all Go/frontend verification commands.**
- [x] **Step 6: Commit with `feat(exploration): initialize blackboard before executor dispatch` and push.**

### Task 8: Default Two-Frontier Parallel Exploration

**Files:**
- Modify: `exploration_runner.go`
- Modify: `exploration_controller.go`
- Modify: `exploration_controller_test.go`
- Modify: `exploration_runner_test.go`
- Modify: `internal/cli/config.go`
- Modify: `internal/cli/config_test.go`
- Modify: `session_agent.go`
- Modify: `examples/run.yaml`
- Modify: `examples/tsec.yaml`
- Modify: `web/src/utils/exploration.ts`
- Modify: `web/src/utils/exploration.test.ts`
- Modify: `web/src/components/ExplorationTimeline.tsx`

**Interfaces:**
- Consumes: `ExplorationSpec.MaxWorkers`, `Controller`, `HypothesisView.current`.
- Produces: default `planner + executor-1 + executor-2`, two concurrently assigned frontiers, and multi-current highlighting in the UI.

- [x] **Step 1: Write failing tests for two default workers, two default executors, and concurrent current frontiers.**
- [x] **Step 2: Change default role expansion, Controller concurrency, CLI defaults and examples to two workers.**
- [x] **Step 3: Track a set of active frontier IDs and highlight both concurrent assignments in the exploration graph.**
- [x] **Step 4: Run all Go/frontend verification commands.**
- [x] **Step 5: Commit with `feat(exploration): run two frontiers concurrently` and push.**
