# First-Principles Architecture Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the contract-first architecture in `docs/superpowers/specs/2026-10-24-first-principles-architecture-design.md` without restoring a second control path.

**Architecture:** Keep the bounded hypothesis-tree engine and existing adapters, but separate stable target authorization from per-run bindings, split lifecycle phase from outcome, route all canonical writes through one redacting audit boundary, stabilize knowledge identity, and migrate Web to the resulting event/result contracts.

**Tech Stack:** Go 1.24, modernc SQLite, Pi RPC, Docker, TSec, React/Vite/TypeScript, Vitest.

**Spec:** `docs/superpowers/specs/2026-10-24-first-principles-architecture-design.md`

## Global Constraints

- One Run has one logical Target and supports only `caller_managed` isolation.
- Only completion authority or the final verifier can produce a successful Run.
- Known secrets never reach canonical records in plaintext.
- SQLite remains canonical; migrations preserve events/logs and are additive/idempotent.
- `agent_settled` is completion; `agent_end` and tool exits are not.
- Unknown Web events must degrade locally, not break the page.
- Docker integration and live TSec/VPN checks are reported only when actually run.

## Review Focus

- A provisioner must not be able to change `TargetSpec.ID`; bindings only add runtime addresses/details.
- Backend completion racing an in-flight attempt must freeze exactly one outcome and leave no post-finalization writes.
- Corroboration must count `(RunID, SessionID)`, not role-local executor names.
- Legacy `status` values must survive schema migration while `phase/outcome` becomes canonical.
- Unknown/old Web event payloads must not crash run detail rendering.

---

### Task 1: Target authorization and binding

**Files:**
- Modify: `types_target.go`, `ports.go`, `types_agent.go`, `types_run.go`
- Modify: `runner.go`, `exploration.go`, `planner.go`, `knowledge.go`, `artifact.go`
- Modify: `sandbox/docker/backend.go`, `tsec/client.go`
- Test: `contract_test.go`, `runner_test.go`, `reachability_test.go`, `artifact_test.go`, backend tests

**Interfaces:**
- Produces: `TargetSpec`, `TargetBinding`, `TargetFingerprint(TargetSpec) string`, `AuthorizeTarget(string, TargetBinding) bool`
- Produces: `TargetProvisioner.Provision(...) (TargetBinding, error)`, `Close(... TargetBinding) error`
- Produces: `CompletionSource.Observe(... TargetBinding) (Completion, error)`

- [x] Add failing contract tests for stable fingerprint, binding identity enforcement, and observation target rejection.
- [x] Run focused tests and verify failure.
- [x] Implement `TargetSpec`/`TargetBinding` and migrate runtime, prompt, verifier and backend signatures.
- [x] Validate every Observation target before Artifact metadata is written.
- [x] Run `go test ./...` and commit.

### Task 2: Lifecycle and attempt semantics

**Files:**
- Modify: `types_run.go`, `types_exploration.go`, `run.go`, `runner.go`, `exploration.go`, `planner.go`, `executor.go`
- Modify: `store/sqlite/store.go`
- Test: `contract_test.go`, `reachability_test.go`, `runner_test.go`, `planner_context_test.go`, `review_test.go`

**Interfaces:**
- Produces: `RunPhase`, `RunOutcome`, `StatusForRunState(RunPhase, RunOutcome) Status`, `RunStateFromStatus(Status) (RunPhase, RunOutcome)`
- Produces: `BranchPhase`, `BranchOutcome`, `AttemptPhase`, `AttemptOutcome`
- Compatibility: `Status` remains a derived read-only projection; no control decision reads it.

- [x] Add failing tests for phase/outcome consistency and `supported` attempt semantics.
- [x] Run focused tests and verify failure.
- [x] Migrate internal transitions to phase/outcome while persisting the compatibility status projection.
- [x] Migrate branch/attempt state transitions and API/DB payloads.
- [x] Run `go test ./...` and commit.

### Task 3: Canonical audit and event contract

**Files:**
- Modify: `ports.go`, `redacting_store.go`, `runner.go`, `run.go`, `exploration.go`
- Modify: `types_run.go`, `store/sqlite/store.go`
- Test: `redacting_store_test.go`, `persistence_failure_test.go`, `store/sqlite/store_v3_test.go`, `contract_test.go`

**Interfaces:**
- Produces: `AuditWriter`, `ReadStore`, `Store` compatibility composition
- Produces: `EventType` constants and typed payload structs for all production events
- Centralizes redaction in the canonical audit writer.

- [x] Add failing tests for universal redaction and unknown event compatibility.
- [x] Run focused tests and verify failure.
- [x] Split read/write interfaces and migrate Runner/Server to narrow dependencies.
- [x] Add typed events and replace production `map[string]any` payloads.
- [x] Add additive SQLite phase/outcome columns while preserving legacy rows.
- [x] Run `go test ./...` and commit.

### Task 4: Knowledge identity and trust

**Files:**
- Modify: `types_knowledge.go`, `knowledge.go`, `planner.go`, `exploration.go`
- Test: `knowledge_test.go`, `planner_context_test.go`, `review_test.go`, `store/sqlite/store_v3_test.go`

**Interfaces:**
- Preserves: `KnowledgeTrust`, `KnowledgeScope`, versioned Knowledge
- Fixes: source independence uses `(RunID, SessionID)`
- Fixes: one stable Target fingerprint implementation and one reuse filter matrix

- [x] Add failing tests for cross-run session identity and stable target filtering.
- [x] Run focused tests and verify failure.
- [x] Implement run-scoped source identity and the documented scope/expiry/trust matrix.
- [x] Run `go test ./...` and commit.

### Task 5: Web and surface migration

**Files:**
- Modify: `web/src/api/types.ts`, `web/src/utils/explorationTree.ts`, `web/src/utils/exploration.ts`, `web/src/components/ExplorationTimeline.tsx`
- Modify: `web/src/components/*.test.tsx`, `web/src/utils/*.test.ts`
- Modify: `server/server.go`, `internal/cli/cli.go`, `README.md`, `docs/architecture.md`, `docs/migration-v1.md`

**Interfaces:**
- Consumes: phase/outcome, typed events, `supported` attempt outcome
- Produces: a single branch/attempt Web projection and safe unknown-event behavior.

- [x] Add failing Web tests for phase/outcome and unknown events.
- [x] Run focused Vitest tests and verify failure.
- [x] Migrate TS types and remove production-unreachable legacy exploration event parsers.
- [x] Update trusted-operator HTTP documentation and loopback assertion.
- [x] Run Web tests, typecheck, lint and build.

### Task 6: Run Controller ownership

**Files:**
- Add: `run_controller.go`, `run_controller_test.go`
- Modify: `run.go`, `runner.go`, `exploration.go`, `runner_test.go`

**Interfaces:**
- Produces: internal `runController` as the single writer for phase/outcome, usage, authority snapshots, sessions and finalization.
- Preserves: `Run` as a facade exposing only `ID`, `Events`, `Done`, `Wait`, `Cancel`, `Steer`.
- Preserves: terminal success authority; only completion or verifier transitions can produce a successful outcome.
- Enforces: canonical event/log appends are serialized against finalization, so `run.finalized` is the last canonical run write.
- Preserves: closed-session usage snapshots for verifier and terminal results.

- [x] Add failing tests for concurrent terminal claims and event writes racing finalization.
- [x] Add tests proving every control mutation enters `runController.apply`.
- [x] Move orchestration and scheduling from `Runner`/`Run` into `runController`.
- [x] Replace direct Branch/Attempt consumers of `Run` state with narrow controller commands and read projections.
- [x] Run focused tests and the full Go suite.

### Task 7: Full verification and delivery

- [x] Run `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./cmd/harness`.
- [x] Run `npm --prefix web run lint`, `typecheck`, `test`, `build`.
- [x] Run `git diff --check` and inspect the staged file list.
- [x] Commit only implementation files and this plan; push the current branch.
