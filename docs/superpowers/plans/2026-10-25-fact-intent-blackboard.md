# Fact / Intent Blackboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the writable Branch/Knowledge exploration path with a two-slot Fact/Intent blackboard while preserving evidence-backed facts and Verifier/backend-only success.

**Architecture:** A single Go Run Controller owns an append-only Fact/Intent board and two fixed Executor slots. Bootstrap produces the first Fact; a serialized Reason task creates at most one Intent per free slot; Explore executes one Intent and uses a same-session Conclude fallback when normal execution times out or returns unusable output.

**Tech Stack:** Go 1.24, modernc SQLite, Pi RPC, Docker, TSec, React/Vite/TypeScript, Vitest.

**Spec:** `docs/superpowers/specs/2026-10-25-fact-intent-blackboard-design.md`

## Global Constraints

- Exactly two Executor Agents; `MaxSessions` must be at least 3.
- `open + claimed Intent <= 2` at all times.
- Reason is the only source of new Intents; Executor cannot create Intents.
- A finding Fact requires at least one archived Observation/Artifact.
- Reason goal candidates never establish success; only Verifier or backend completion can.
- Conclude fallback only runs when the same Agent session is still alive.
- New Runs do not write Branch or Knowledge control records.
- Old Branch/Knowledge records remain readable; migrations are additive and never delete audit rows.
- Known secrets are redacted before every canonical write.
- Docker integration and live TSec checks are reported only when actually run.

## Review Focus

- Reason returns more Intents than free slots: accept only free-slot count and audit the rest.
- A slot is refilled while Reason is in flight: stale frontier version must not create a third open Intent.
- Execute times out but the session survives: Conclude must run and produce evidence or a negative Fact without further exploration.
- Backend completion races both slots and Reason: one terminal authority must win and no post-finalization writes may occur.
- A positive Fact lacks evidence or Reason claims goal: reject the fact/continue exploration; never synthesize success.

---

### Task 1: Blackboard Contracts and Policy

**Files:**
- Create: `types_blackboard.go`
- Create: `types_blackboard_test.go`
- Modify: `types_policy.go`
- Modify: `types_run.go`

**Interfaces:**
- Produces: `FactKind`, `Fact`, `IntentKind`, `IntentState`, `Intent`, `Hint`, `IntentAttemptPhase`, `IntentAttempt`, `FactProposal`, `ExploreReport`, `IntentProposal`, `GoalCandidate`, `ReasonReport`, `ReasonRequest`, `ExploreRequest`, `ReasonStrategy`, `ExploreStrategy`.
- Produces: `ValidateFact(Fact) error`, `ValidateIntentTransition(from, to IntentState) error`, `ValidateReasonReport(ReasonReport, int) error`, `ValidateExploreReport(ExploreReport) error`.
- Produces policy fields: `MaxOpenIntents`, `MaxTotalIntents`, `MaxAttemptsPerIntent`, `MaxToolCallsPerAttempt`, `MaxWallTimePerAttempt`, `ConcludeTimeout`, `StagnationRounds`, `MaxPlannerTools`, `MaxPlannerWallTime`, `BootstrapEnabled`.

- [ ] **Step 1: Write failing blackboard contract tests**

Create `types_blackboard_test.go` with:
- `TestFindingFactRequiresObservation`: a `FactFinding` with no ObservationIDs fails; with one passes.
- `TestReasonReportCannotExceedFreeSlots`: 3 Intents with `freeSlots=2` fails `ValidateReasonReport`; 2 passes.
- `TestReasonReportRejectsIntentsAndGoalCandidateTogether`: both populated fails.
- `TestIntentTransitions`: `open -> claimed -> concluded`, `claimed -> open`, and `claimed -> abandoned` pass; `concluded -> claimed` fails.
- `TestExploreReportOnlyProducesFindingOrNegative`: `FactOrigin`, `FactGoal`, empty Fact, or finding without Observations fail.
- `TestBlackboardPolicyDefaults`: `MaxOpenIntents == 2`, `BootstrapEnabled == true`, positive attempt/conclude limits.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./... -run 'TestFindingFact|TestReasonReport|TestIntentTransitions|TestExploreReport|TestBlackboardPolicy'`
Expected: FAIL with missing types/fields.

- [ ] **Step 3: Implement blackboard types and validators**

Add the types exactly as named in the spec. Use controller-generated IDs and run-scoped fact/intent IDs. Keep legacy `Branch`, `Attempt`, and `Knowledge` types in place until Task 6 so the repository remains buildable.

Policy changes must be additive in this task; keep deprecated Branch fields temporarily for existing tests.

- [ ] **Step 4: Run the focused and full Go tests**

Run: `go test ./... -run 'TestFindingFact|TestReasonReport|TestIntentTransitions|TestExploreReport|TestBlackboardPolicy' && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit and push**

```bash
git add types_blackboard.go types_blackboard_test.go types_policy.go types_run.go
git commit -m "feat(blackboard): define fact intent contracts"
git push
```

### Task 2: SQLite Graph Persistence

**Files:**
- Create: `store/sqlite/blackboard.go`
- Create: `store/sqlite/blackboard_test.go`
- Modify: `ports.go`
- Modify: `store/sqlite/store.go`
- Modify: `redacting_store.go`
- Modify: `redactor.go`

**Interfaces:**
- Produces: `BoardWriter` and `BoardReader` interfaces:
  - `SaveFact(context.Context, Fact) error`
  - `SaveIntent(context.Context, Intent) error`
  - `SaveHint(context.Context, Hint) error`
  - `SaveIntentAttempt(context.Context, IntentAttempt) error`
  - `SaveBlackboardObservation(context.Context, Observation) error`
  - `ListFacts(context.Context, string) ([]Fact, error)`
  - `ListIntents(context.Context, string) ([]Intent, error)`
  - `ListHints(context.Context, string) ([]Hint, error)`
  - `ListIntentAttempts(context.Context, string) ([]IntentAttempt, error)`
  - `ListBlackboardObservations(context.Context, string) ([]Observation, error)`
- `Store` composes `BoardWriter`; `ReadStore` composes `BoardReader`.
- SQLite schema version becomes `5`.

- [ ] **Step 1: Write failing persistence tests**

Create `store/sqlite/blackboard_test.go` with:
- `TestBlackboardRoundTrip`: save/list facts, intents, hints, intent attempts, and observations per Run.
- `TestBlackboardRunIsolation`: identical intent IDs in two Runs remain distinct.
- `TestBlackboardSchemaPreservesLegacyRows`: open a database containing legacy branch/knowledge rows and assert row counts are unchanged after opening.
- `TestBlackboardIntentSources`: an Intent with multiple `FromFactIDs` round-trips in deterministic order.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./store/sqlite -run 'TestBlackboard|TestBlackboardSchema'`
Expected: FAIL with missing schema/methods.

- [ ] **Step 3: Implement additive schema v5**

Add tables:

```text
facts
intents
intent_sources
hints
exploration_attempts_v2
exploration_observations_v2
exploration_observation_artifacts_v2
```

Use composite keys including `run_id`. Never drop or rewrite legacy tables.

- [ ] **Step 4: Implement store methods and redaction**

Implement the `BoardWriter`/`BoardReader` methods in `store/sqlite/blackboard.go`. Add redaction for Fact.Description, Intent.Description/Error, Hint.Content, IntentAttempt.Error, and Observation text fields in `redacting_store.go`.

- [ ] **Step 5: Run persistence and redaction tests**

Run: `go test ./store/sqlite ./... -run 'TestBlackboard|TestRedacting|TestMigration'`
Expected: PASS.

- [ ] **Step 6: Commit and push**

```bash
git add store/sqlite/blackboard.go store/sqlite/blackboard_test.go store/sqlite/store.go ports.go redacting_store.go redactor.go
git commit -m "feat(sqlite): persist fact intent board"
git push
```

### Task 3: Pure Board Reducer and Two-Slot Admission

**Files:**
- Create: `blackboard.go`
- Create: `blackboard_internal_test.go`
- Create: `blackboard_test.go`

**Interfaces:**
- Produces internal `boardState`:
  - `newBoardState(runID string, spec RunSpec, target TargetBinding) *boardState`
  - `snapshot() boardSnapshot`
  - `addFact(Fact) error`
  - `createIntent(Intent) error`
  - `claimIntent(intentID string, slot int, executorID string, at time.Time) (Intent, error)`
  - `returnIntentToOpen(intentID string, at time.Time) (Intent, error)`
  - `concludeIntent(intentID, factID string, at time.Time) (Intent, error)`
  - `abandonIntent(intentID, reason string, at time.Time) (Intent, error)`
  - `openCount() int`, `claimedCount() int`, `frontierVersion() uint64`
- Produces `admitReasonIntents(state *boardState, report ReasonReport, freeSlots []int) ([]Intent, error)`.

- [ ] **Step 1: Write failing reducer/admission tests**

Add:
- `TestBoardStartsWithOriginAndGoal`
- `TestBoardRejectsThirdOpenIntent`
- `TestBoardRejectsIntentClaimOnOccupiedSlot`
- `TestBoardConcludeBindsFactAndReleasesSlot`
- `TestBoardRetryReturnsIntentToOpen`
- `TestReasonAdmissionCapsAtFreeSlots`
- `TestReasonAdmissionRejectsStaleVersion`
- `TestReasonAdmissionNeverExceedsTwoClaimed`
- `TestBoardRejectsFindingWithoutEvidence`

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./... -run 'TestBoard|TestReasonAdmission'`
Expected: FAIL with missing reducer.

- [ ] **Step 3: Implement reducer and admission**

Keep all mutations in `boardState`; methods return updated immutable views. Increment `frontierVersion` on every Intent state change or Fact addition. Reject extra proposals without appending them.

- [ ] **Step 4: Run race-safe focused tests**

Run: `go test ./... -run 'TestBoard|TestReasonAdmission' && go test -race ./... -run 'TestBoard|TestReasonAdmission'`
Expected: PASS.

- [ ] **Step 5: Commit and push**

```bash
git add blackboard.go blackboard_internal_test.go blackboard_test.go
git commit -m "feat(blackboard): enforce two slot frontier"
git push
```

### Task 4: Bootstrap and Reason Tasks

**Files:**
- Create: `reason.go`
- Create: `reason_test.go`
- Create: `bootstrap.go`
- Create: `bootstrap_test.go`
- Modify: `types_blackboard.go`

**Interfaces:**
- Produces `defaultReasonStrategy` implementing `ReasonStrategy`.
- Produces `defaultBootstrapStrategy`:
  - `BootstrapPrompt(ExploreRequest) (string, error)`
  - `ParseBootstrap(ExploreRequest, TurnResult) (ExploreReport, error)`
- Produces `buildReasonContext(boardSnapshot, target TargetBinding, hints []Hint, freeSlots []int, recent []Intent, policy RunPolicy) ReasonRequest`.

- [ ] **Step 1: Write failing Reason/Bootstrap tests**

Add:
- `TestReasonPromptIncludesOnlyBoundedGraph`
- `TestReasonPromptIncludesFreeSlotCount`
- `TestReasonPromptRejectsCompletionAuthority`
- `TestParseReasonReportStrictJSON`
- `TestReasonReportTruncatesOrRejectsExtraIntents` (use validator, assert no third Intent).
- `TestBootstrapPromptStartsWithOriginGoalAndHints`
- `TestBootstrapReportCannotCreateChildIntents`

- [ ] **Step 2: Run and verify failure**

Run: `go test ./... -run 'TestReason|TestBootstrap'`
Expected: FAIL with missing strategies.

- [ ] **Step 3: Implement prompts and parse paths**

Reason output must be one JSON object with `frontier_version`, `intents`, and optional `goal_candidate`. Bootstrap output uses the ExploreReport schema, but its only allowed fact kinds are `finding` and `negative`.

- [ ] **Step 4: Run focused/full tests**

Run: `go test ./... -run 'TestReason|TestBootstrap' && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit and push**

```bash
git add reason.go reason_test.go bootstrap.go bootstrap_test.go types_blackboard.go
git commit -m "feat(reason): add fact graph planning tasks"
git push
```

### Task 5: Explore Execution and Conclude Fallback

**Files:**
- Create: `explore.go`
- Create: `explore_test.go`
- Modify: `executor.go`
- Modify: `types_blackboard.go`

**Interfaces:**
- Produces `defaultExploreStrategy` implementing `ExploreStrategy`.
- Produces `runExploreAttempt(context.Context, AgentSession, ExploreRequest, TurnLimits, time.Duration, EventSink, ExploreStrategy) (ExploreReport, string, error)` where the string is `execute` or `conclude`.
- Produces `conclude` path that uses an independent timeout derived from the live Run context.

- [ ] **Step 1: Write failing Explore/Conclude tests**

Add:
- `TestExploreParseRequiresFindingEvidence`
- `TestExploreParseAcceptsNegativeFactWithoutEvidence`
- `TestConcludeRunsAfterExecuteTimeout`
- `TestConcludeDoesNotRunAfterProcessExit`
- `TestConcludeCannotExceedItsTimeout`
- `TestConcludeReportCannotCreateIntentOrKnowledge`

- [ ] **Step 2: Run and verify failure**

Run: `go test ./... -run 'TestExploreParse|TestConclude'`
Expected: FAIL.

- [ ] **Step 3: Implement execute and conclude prompts**

Execute prompt must tell the agent to stay on one Intent and write evidence. Conclude prompt must forbid new tools/scans and request JSON immediately. If execute times out but the session is alive, run conclude; if the session is dead, return process failure.

- [ ] **Step 4: Run focused/full/race tests**

Run: `go test ./... -run 'TestExploreParse|TestConclude' && go test ./... && go test -race ./... -run 'TestConclude|TestExplore'`
Expected: PASS.

- [ ] **Step 5: Commit and push**

```bash
git add explore.go explore_test.go executor.go types_blackboard.go
git commit -m "feat(explore): add conclude fallback"
git push
```

### Task 6: Two-Slot Engine and Run Controller Integration

**Files:**
- Create: `blackboard_engine.go`
- Create: `blackboard_engine_test.go`
- Modify: `exploration.go`
- Modify: `run_controller.go`
- Modify: `runner.go`
- Modify: `run.go`
- Modify: `types_run.go`
- Modify: `types_policy.go`

**Interfaces:**
- Produces `blackboardEngine` with `run(context.Context) error`.
- Controller owns `board *boardState`, two slots, and one serialized Reason state.
- `RunSpec.PlannerStrategy` becomes `ReasonStrategy`; `RunSpec.ExecutorStrategy` becomes `ExploreStrategy`.
- `RunSpec` requires exactly two Executors.
- Result gains `Facts`, `Intents`, `Hints` fields while retaining legacy fields for old Runs.

- [ ] **Step 1: Write failing engine/controller tests**

Add:
- `TestEngineBootstrapBeforeReason`
- `TestEngineReasonFillsOnlyFreeSlots`
- `TestEngineRefillsSingleSlotWithoutBarrier`
- `TestEngineStaleReasonCannotCreateThirdIntent`
- `TestEngineBackendCompletionCancelsTwoSlotsAndReason`
- `TestEngineStopsAfterStagnation`
- `TestEngineRequiresTwoExecutors`

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./... -run 'TestEngine'`
Expected: FAIL with missing engine and fields.

- [ ] **Step 3: Implement blackboard engine**

Use a single event loop with two slot workers. Reason turns are serialized. A slot releases only after evidence/Fact merge and intent conclusion. No round barrier. All board mutations flow through controller transitions.

- [ ] **Step 4: Replace production runner path**

Switch `runController.run` from `explorationEngine` to `blackboardEngine`. Keep legacy Branch types and store methods reachable only through historical reads. Remove production calls to `runBranches`, `PromoteKnowledge`, review branches, and branch persistence.

- [ ] **Step 5: Run full and race tests**

Run: `go test ./... && go test -race ./...`
Expected: PASS.

- [ ] **Step 6: Commit and push**

```bash
git add blackboard_engine.go blackboard_engine_test.go exploration.go run_controller.go runner.go run.go types_run.go types_policy.go
git commit -m "refactor(core): run two slot blackboard engine"
git push
```

### Task 7: Events, HTTP Graph API, and Web Graph

**Files:**
- Modify: `events.go`
- Modify: `ports.go`
- Modify: `server/server.go`
- Modify: `server/server_v1_test.go`
- Modify: `web/src/api/types.ts`
- Modify: `web/src/api/client.ts`
- Modify: `web/src/hooks/useRunEvents.ts`
- Create: `web/src/utils/blackboardGraph.ts`
- Create: `web/src/utils/blackboardGraph.test.ts`
- Create: `web/src/components/FactIntentGraph.tsx`
- Modify: `web/src/pages/RunDetailPage.tsx`
- Modify: `web/src/components/AppShell.tsx` only if navigation copy needs it

**Interfaces:**
- Produces typed events: `fact.created`, `hint.created`, `reason.*`, `intent.*`, `attempt.*`, `verification.rejected`.
- Produces `GET /v1/runs/{id}/graph` returning `{facts, intents, hints, slots, reason}`.
- Produces `extractBlackboardView(events: EventRecord[]): BlackboardView`.
- Old Branch/Knowledge events remain readable.

- [ ] **Step 1: Write failing event/API/Web tests**

Add:
- `TestNewEventsRejectUnknownPayload`
- `TestGraphEndpointReturnsFactsAndSlots`
- `TestWebContractMatchesGoEvents` updates for new events.
- Vitest: `extractBlackboardView` builds Fact/Intent nodes, ignores malformed/old events.
- Vitest: graph shows `slot-1`, `slot-2`, active claimed Intent, Reason status, and Conclude marker.

- [ ] **Step 2: Run and verify failure**

Run: `go test ./... -run 'TestNewEvents|TestGraphEndpoint|TestWebContract' && npm --prefix web run test -- blackboardGraph`
Expected: FAIL.

- [ ] **Step 3: Implement typed events and graph endpoint**

Register each new event type in `events.go`; add payload types. Add ReadStore graph reads and HTTP endpoint. Do not remove old event types.

- [ ] **Step 4: Implement Web graph**

Render new Runs with Fact nodes and Intent edges, two slot cards, Reason status, Hints, and evidence badges. Keep old Branch/Knowledge view for old event streams. Unknown events must degrade locally.

- [ ] **Step 5: Run Go/Web verification**

Run:
```bash
go test ./...
go test -race ./... -run 'TestGraph|TestBlackboard|TestEngine'
go vet ./...
go build ./cmd/harness
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
```
Expected: PASS.

- [ ] **Step 6: Commit and push**

```bash
git add events.go ports.go server web/src
git commit -m "feat(surfaces): visualize fact intent graph"
git push
```

### Task 8: Legacy Retirement, Migration Docs, and Live Smoke

**Files:**
- Modify: `README.md`
- Modify: `docs/architecture.md`
- Modify: `docs/migration-v1.md`
- Modify: `docs/tsec.md`
- Modify: `examples/tsec.yaml`
- Modify: `examples/run.yaml`
- Delete/retire: old writable Branch/Knowledge control code that no longer has production callers
- Modify: `architecture_contract_test.go`, `architecture_convergence_test.go`, `import_boundary_test.go`, `reachability_test.go`
- Maybe create: `docs/migration-v2.md`

**Interfaces:**
- New Runs and APIs expose only Fact/Intent semantics.
- Old Branch/Knowledge records remain read-only.
- No production symbol creates `BranchChild`, `KnowledgeContested`, or review branches.

- [ ] **Step 1: Add failing legacy-retirement tests**

Add:
- `TestNewRunDoesNotWriteLegacyBranchTables`
- `TestNewRunDoesNotWriteKnowledgeTables`
- `TestNoWritableKnowledgeControlPath`
- `TestCLIFailsClosedOnLegacyExplorationKeys`
- `TestOldEventsStillRender`
- `TestTSecConfigUsesTwoExecutorsAndBootstrap`

- [ ] **Step 2: Run and verify failure**

Run: `go test ./... -run 'TestNewRunDoesNotWrite|TestNoWritable|TestOldEvents|TestTSecConfig'`
Expected: FAIL until cleanup/migration.

- [ ] **Step 3: Retire writable legacy control code**

Remove only production-unreachable old scheduler, branch expansion, knowledge promotion/review, and child-branch paths. Keep legacy store read methods and legacy event reducers required for historical Runs. Update the AST/import tests to enforce the new boundary.

- [ ] **Step 4: Update docs and examples**

Document:
- Bootstrap/Reason/Explore/Conclude.
- Fact/Intent/Hint graph.
- Two-slot invariant.
- Fact evidence requirements.
- Old Branch/Knowledge read-only status.
- Migration from Branch/Knowledge to Fact/Intent.
- No model-generated success.

- [ ] **Step 5: Run the full matrix**

Run:
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
Expected: PASS. Do not claim Docker/live TSec unless actually run.

- [ ] **Step 6: Run a real TSec smoke test**

Use the configured local `.env` and VPN. Run at least one easy challenge with the two-slot engine. Record:
- result status and score;
- elapsed time;
- model calls/tool calls/tokens/cost;
- open/claimed Intent high-water mark;
- conclude fallback count.

If the external platform/model is unavailable, report the exact failure instead of claiming success.

- [ ] **Step 7: Commit and push**

```bash
git add README.md docs/architecture.md docs/migration-v1.md docs/tsec.md examples architecture_contract_test.go architecture_convergence_test.go import_boundary_test.go reachability_test.go
git commit -m "refactor(core): retire branch knowledge control"
git push
```

## Self-Review Checklist

- [ ] Every spec section maps to a task above.
- [ ] All new public names are consistent across tasks: `Fact`, `Intent`, `Hint`, `IntentAttempt`, `ReasonReport`, `ExploreReport`, `BoardWriter`, `BoardReader`.
- [ ] The five Review Focus cases have named tests in the owning tasks.
- [ ] Old data remains read-only and test-covered.
- [ ] No task relies on a model-generated success verdict.
- [ ] No task leaves a third open/claimed Intent possible.
- [ ] Each task ends with a focused test, full relevant suite, commit, and push.
