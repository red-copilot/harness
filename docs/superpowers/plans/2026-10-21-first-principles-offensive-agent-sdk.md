# First-Principles Offensive Agent SDK v1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn Red Harness into a pure Go, single-Run/single-Target offensive-security agent SDK whose exploration, evidence, knowledge, audit and verdict invariants are mechanically enforced.

**Architecture:** The root `harness` package owns public contracts, lifecycle, the fixed exploration engine, evidence and knowledge semantics. Pi, Docker, TSec, SQLite, CLI, HTTP and Web are adapters. The engine uses round barriers, a single-writer collector, content-addressed Artifacts and code-owned trust transitions; only a Verifier or backend completion can establish success.

**Tech Stack:** Go 1.24, Pi RPC over strict LF JSONL, Docker, SQLite (`modernc.org/sqlite`), React/Vite/TypeScript, Vitest.

**Spec:** `docs/superpowers/specs/2026-10-21-first-principles-offensive-agent-sdk-design.md`

## Global Constraints

- One Run has exactly one logical `Target`; `Target.Isolation` is `caller_managed` in v1 and the caller owns network ACLs.
- Only `Verifier` or backend completion may produce a successful verdict.
- Mechanical invariants are code-owned: branch factor 2, default active branches 2, depth 4, nodes 15, attempts per branch 3, tool calls per attempt 20, attempt wall time 10 minutes and stagnation rounds 3.
- Knowledge prompt bounds are 8 items, 200 bytes per statement and 2048 bytes total.
- `KnowledgeTrust` contains only `observed`, `corroborated`, `contested`, `refuted`; `verified` knowledge must not exist.
- Knowledge cross-Run reuse is limited to the same `Task.ID`.
- Attempt evidence is copied into content-addressed storage; only explicit workspace files are archived.
- Known secrets must be redacted before every event, log, normalized-state, Artifact, result, verdict or error write.
- SQLite is the canonical audit store; migrations never delete historical event/log rows.
- Containers never receive the Docker socket and keep read-only root, `no-new-privileges`, capability dropping and resource limits.
- Pi uses strict LF JSONL framing and `agent_settled` as turn completion; `agent_end` is not completion.
- The `harness` root package must compile using only the Go standard library.
- The v1 API is a clean break; no long-lived compatibility wrappers for `Workflow`, `PiSpec`, `Exploration.Configure` or `KnowledgeVerified`.
- Every task follows red-green TDD, runs its named verification, and commits only files belonging to that task with a Conventional Commit and scope.
- Final verification must run `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./cmd/harness`, `npm --prefix web run lint`, `npm --prefix web run typecheck`, `npm --prefix web run test` and `npm --prefix web run build`.
- Docker integration and live TSec/VPN tests are environment-dependent and must not be reported as passing unless actually run.

## Review Focus

1. Cancellation can close an event channel while a session writer or log producer is still active; tests must prove no writes after closure and no `close of closed channel` panic.
2. Two Runs sharing one SQLite database can collide on root/branch/attempt/observation IDs or leak knowledge across `Task.ID`; tests must cover cross-Run isolation and stable/instance/never reuse.
3. A malicious report can reference `../`, absolute paths, symlinks, oversized files or fabricated hashes; tests must prove all are rejected before any Artifact metadata or bytes are persisted.
4. A known secret can bypass redaction through normalized state, text Artifact content, errors or verdict evidence; tests must scan every SQLite table and artifact store after the run.
5. The implementation can accidentally imply that SDK semantic Target checks enforce network isolation; README, CLI/HTTP examples and tests must preserve the `caller_managed` language and reject unsupported isolation modes.

## Invariant Coverage

| Constraint | Owning task | Proof tests |
|---|---|---|
| C1 authoritative success | Task 1, Task 9 | `TestRunnerUsesVerifierAsOnlySuccessSource`, `TestEndToEndPlannerExecutorEvidenceKnowledgeVerdict` |
| C2 mechanical invariants | Task 1, Task 2 | `TestChildLimitIsMechanical`, `TestMaxActiveBranchesIsConcurrent`, `TestKnowledgeDigestHonorsCountAndByteLimits` |
| C3 evidence before knowledge | Task 3, Task 4 | `TestKnowledgeRequiresObservationFromCurrentAttempt`, `TestArchiveEvidenceRequiresObservationFile` |
| C4 immutable audit | Task 5, Task 9 | `TestMigrationPreservesAuditRows`, `TestKnowledgeVersionsAreAppendOnly` |
| C5 fail closed | Task 1, Task 5, Task 9 | `TestRunnerPersistsFailureWhenAuditWriteFails`, `TestStoreWriteFailureStopsRun`, `TestMalformedExecutorReportRetriesThenBlocks` |
| C6 secret boundary | Task 3, Task 5, Task 7 | `TestTextArtifactIsRedactedBeforeHash`, `TestRedactingStoreCoversAllTables`, `TestTSecTokenNeverPersisted` |
| C7 container hardening | Task 7, Task 9 | `TestDockerSpecUsesHardenedDefaults`, `TestFactoryNeverReceivesDockerSocket` |
| C8 single control semantics | Task 1, Task 9 | `TestNoLegacyControlSymbols`, `TestRootPackageImportsStdlibOnly` |
| C9 single Run/single Target | Task 1 | `TestValidateRunSpecRequiresTaskAndTarget`, `TestRunDescriptorRoundTrip` |
| C10 no implicit recovery | Task 1 | `TestRunnerMarksUnfinishedRunsInterrupted` |
| C11 bounded model context | Task 2, Task 4 | `TestKnowledgeDigestHonorsCountAndByteLimits`, `TestPlannerAndExecutorReceiveOnlyBoundedRecentReports` |
| C12 every state reachable | Task 4, Task 9 | `TestNoDeclaredUnreachableState`, `TestKnowledgeTrustHasNoVerified` |

---

## File Structure

Root `harness`:

- `types.go` — public domain primitives, statuses, budgets, policies and Results.
- `ports.go` — runtime, backend, verifier, Store and ArtifactStore interfaces.
- `run.go` — live Run handle and public control methods.
- `runner.go` — Run lifecycle, target provisioning, cancellation, cleanup and verdict finalization.
- `planner.go` — Plan request/proposal types and planner strategy contract.
- `executor.go` — Attempt request/report, observation proposal and executor strategy contract.
- `exploration.go` — fixed round-barrier exploration scheduler and validation.
- `knowledge.go` — trust transitions, conflict review, fingerprints, reuse and bounded digests.
- `artifact.go` — evidence path validation, archive validation and text redaction.
- `redactor.go` — known-secret redaction primitives.

Adapters:

- `pi/config.go`, `pi/session.go` — Pi runtime configuration and protocol.
- `sandbox/docker/backend.go`, `sandbox/docker/runtime.go` — hardened process/runtime infrastructure.
- `tsec/client.go` — TSec API and Backend.
- `verify/command.go` — command verifier.
- `store/sqlite/store.go`, `store/sqlite/exploration.go`, `store/sqlite/artifact.go` — canonical metadata and local content-addressed Artifact storage.
- `server/server.go` — descriptor HTTP/SSE adapter.
- `internal/cli/*.go` — descriptor composition and commands.
- `web/src/**` — event mapping and read-only console.

Deleted in Task 1 or Task 9 as specified:

- `workflow.go`, `workflow_test.go`
- the current `exploration/` package and its tests after behavior is moved into root files
- core `PiSpec`, `Workflow`, `WorkflowRuntime`, `WorkflowConfig` and duplicate exploration control types

---

### Task 1: Public Contracts, Run Lifecycle and Fixed Exploration Engine

**Files:**
- Replace: `types.go`
- Create: `ports.go`, `planner.go`, `executor.go`, `exploration.go`
- Modify: `run.go`, `runner.go`, `redactor.go`
- Delete: `workflow.go`, `workflow_test.go`, `exploration/model.go`, `exploration/engine.go`, `exploration/session.go`, `exploration/turn.go`, `exploration/workflow.go` and their tests after behavior is ported
- Test: `contract_test.go`, `runner_test.go`, `exploration_test.go`

**Interfaces:**
- Consumes: existing `ProcessStarter`, Docker/Tsec lifecycle behavior and SQLite-backed Store concepts as reference.
- Produces:
  - `type RunSpec struct { Task Task; Target Target; Planner AgentSpec; Executors []AgentSpec; Policy ExplorationPolicy; Budget Budget; Backend Backend; Verifier Verifier; Artifacts ArtifactStore; SecretEnv []string; WorkspaceRoot string; PlannerStrategy PlannerStrategy; ExecutorStrategy ExecutorStrategy }`
  - `func NewRunner(Config) (*Runner, error)`
  - `func (r *Runner) Start(context.Context, RunSpec) (*Run, error)`
  - `func (r *Runner) Run(context.Context, RunSpec) (Result, error)`
  - `func (r *Run) Wait(context.Context) (Result, error)`
  - `func (r *Run) Cancel(context.Context) error`
  - `func (r *Run) Steer(context.Context, sessionID, message string) error`
  - `type AgentRuntimeFactory interface { NewSession(context.Context, RuntimeRequest) (AgentSession, error) }`
  - `type AgentSession interface { Prompt(context.Context, string) error; Steer(context.Context, string) error; Reset(context.Context) error; Events() <-chan AgentEvent; Logs() <-chan LogEvent; Usage() Usage; Abort(context.Context) error; Close(context.Context) error }`
  - `type PlannerStrategy interface { Prompt(PlanRequest) (string, error); Parse(PlanRequest, TurnResult) (PlanProposal, error) }`
  - `type ExecutorStrategy interface { Prompt(AttemptRequest) (string, error); Parse(AttemptRequest, TurnResult) (AttemptReport, error) }`
  - `func DefaultExplorationPolicy() ExplorationPolicy`

- [ ] **Step 1: Write failing contract and lifecycle tests**

Add `contract_test.go` with:
- `TestRunDescriptorRoundTrip`: JSON round-trip of a `RunDescriptor` preserves `Task.ID`, one `Target`, planner, two executors, policy and budget; interface fields serialize as `null`/absent.
- `TestValidateRunSpecRequiresTaskAndTarget`: empty `Task.ID`, empty `Task.Objective`, empty `Target.ID`, unsupported isolation and no Executor each return an error containing the field name.
- `TestValidateRunSpecRequiresRuntimeAndArtifacts`: nil planner/executor runtime, empty executor ID and nil `ArtifactStore` are rejected before Store writes.
- `TestValidateRunSpecRequiresAuthority`: a spec with nil `Verifier` and nil `Backend` is rejected.
- `TestDefaultExplorationPolicy`: all defaults in the spec table are exact.
- `TestKnowledgeTrustHasNoVerified`: a compile-time string check confirms constants are only `observed`, `corroborated`, `contested`, `refuted`.

Add `runner_test.go` lifecycle cases using fake Store, fake ArtifactStore, fake Backend, fake Verifier and fake runtime/session:
- `TestRunnerUsesVerifierAsOnlySuccessSource`: a session that emits fabricated success text still yields `StatusFailed` unless the Verifier returns `Success=true`.
- `TestBackendCompletionWinsAndCancelsWork`: backend completion closes the run as succeeded, cancels a blocked exploration path and preserves the backend verdict.
- `TestRunnerPersistsFailureWhenAuditWriteFails`: `AppendEvent` failure makes the Run failed and prevents a success result.
- `TestRunnerCancelClosesSessionsAndTarget`: cancellation calls every session `Abort`/`Close` and backend `Close`.
- `TestRunnerMarksNoAuthoritativeVerdictAsFailed`: completed exploration without verifier/backend fails with `run has no authoritative verdict`.
- `TestRunnerMarksUnfinishedRunsInterrupted`: startup recovery marks queued/running records `interrupted` and never resumes them.

Add `exploration_test.go` with:
- `TestPlannerCannotCreateNonRootBranch`: one valid root child and one non-root child produce one Branch and one `exploration.branch.rejected` event.
- `TestExpansionRequiresLocalSuccessAndObservation`: completed/inconclusive and local-success-without-observation attempts create zero children.
- `TestChildLimitIsMechanical`: three child proposals create two Branches and one rejection.
- `TestMaxActiveBranchesIsConcurrent`: with `MaxActiveBranches=2`, two fake Executors signal both attempts started before either is released.
- `TestAgentEndDoesNotCompleteTurn`: a stream containing `agent_end` but no `agent_settled` fails the Attempt.
- `TestEventStreamCloseBeforeSettlementFailsAttempt`: closed event stream produces `AttemptFailure`.

- [ ] **Step 2: Run the new tests to verify they fail**

Run: `go test ./... -run 'TestRunDescriptorRoundTrip|TestValidateRunSpec|TestDefaultExplorationPolicy|TestRunner|TestPlanner|TestExpansion|TestChildLimit|TestMaxActiveBranches|TestAgentEnd|TestEventStreamClose'`

Expected: FAIL because the new types, methods and fixed engine do not exist.

- [ ] **Step 3: Implement public contracts and ports**

Replace `types.go` with the exact public types from the spec:
- statuses including `StatusProvisioning` and `StatusFinalizing`
- `Task`, `Target`, `IsolationMode`, `AgentSpec`, `ResourceSpec`, `Budget`, `Usage`
- `BranchKind`, `BranchStatus`, `AttemptStatus`, `AttemptOutcome`
- `ObservationProposal`, `Observation`, `AttemptReport`, `PlanRequest`, `PlanProposal`, `AttemptRequest`
- `KnowledgeScope`, `KnowledgeTrust`, `ReusePolicy`, `KnowledgeProposal`, `KnowledgeSource`, `Knowledge`, `KnowledgeQuery`
- `Artifact`, `ArtifactInput`, `RunDescriptor`, `RunRecord`, `Result`, `Verdict`, `Event`, `LogRecord`, `SessionResult`, `Completion`
- `ExplorationPolicy` with defaults 4/15/2/2/3/20/10m/3/4/2m/200/200/8/2048/1 MiB/16/10 MiB/50 MiB.

Create `ports.go` with `AgentRuntimeFactory`, `AgentSession`, `Process`, `ProcessStarter`, `Backend`, `Verifier`, `Store`, `ArtifactStore`, `ResourceCleaner` and `EventSink`. `Store` must include the methods in spec §12.1. Remove `Workflow`, `WorkflowRuntime`, `WorkflowConfig`, `WorkflowResult`, `PiSpec`, `Task.Targets`, `KnowledgeVerified` and `AgentSession.Wait`.

- [ ] **Step 4: Implement Run lifecycle and fixed engine skeleton**

Rewrite `run.go` and `runner.go` so `Runner` owns:
- validation and normalization
- target provisioning
- workspace/skills preparation
- planner/executor session creation through `AgentRuntimeFactory`
- one fixed exploration execution path
- backend completion race
- budget enforcement
- cancellation and ordered cleanup
- verifier invocation
- verdict/result/event persistence

Rewrite `planner.go`, `executor.go` and `exploration.go` to implement:
- root creation
- planner root-level proposals
- deterministic ready ordering
- round-barrier concurrent Attempts up to `MaxActiveBranches`
- single-writer merge after each batch
- child expansion only after a completed `local_success` Attempt with an Observation
- max two hypothesis children per branch, root/review exemptions
- depth/node limits
- attempt and turn budgets
- stagnation after three rounds without progress/knowledge
- backend completion cancellation
- no writes after event-channel closure

Use only internal `explorer` seams needed by tests; do not export a replacement `Workflow` interface.

- [ ] **Step 5: Update all repository call sites to compile against v1**

Update temporarily portable adapter code in `pi/session.go`, `sandbox/docker/*.go`, `tsec/client.go`, `verify/command.go`, `store/sqlite/*.go`, `server/server.go` and `internal/cli/*.go` to the new interfaces. Detailed adapter behavior is owned by later tasks; this step only removes old symbol dependencies and keeps the tree buildable.

Delete `workflow.go`, `workflow_test.go` and the old `exploration/` package files only after their required behavior has moved into root tests and implementation.

- [ ] **Step 6: Run focused tests to verify they pass**

Run: `go test ./... -run 'TestRunDescriptorRoundTrip|TestValidateRunSpec|TestDefaultExplorationPolicy|TestRunner|TestPlanner|TestExpansion|TestChildLimit|TestMaxActiveBranches|TestAgentEnd|TestEventStreamClose'`

Expected: PASS with zero failures.

- [ ] **Step 7: Run the package suite and commit**

Run: `go test ./...`

Expected: PASS for every package that remains.

```bash
git add types.go ports.go planner.go executor.go exploration.go run.go runner.go redactor.go contract_test.go runner_test.go exploration_test.go pi sandbox tsec verify store server internal/cli
git commit -m "refactor(core): establish exploration-first agent sdk"
```

---

### Task 2: Bounded Context and Structured Agent Turn Protocol

**Files:**
- Modify: `planner.go`, `executor.go`, `exploration.go`, `ports.go`
- Test: `planner_test.go`, `executor_test.go`, `context_test.go`

**Interfaces:**
- Consumes: `PlanRequest`, `AttemptRequest`, `PlannerStrategy`, `ExecutorStrategy`, `AgentSession`, `AgentRole`.
- Produces:
  - `func BuildPlannerContext(Task, Target, []Branch, []Knowledge, Completion, []Attempt) (PlanRequest, int)`
  - `func BuildExecutorContext(Task, Target, Branch, []Knowledge, AttemptBudget, string) (AttemptRequest, int)`
  - `func RunAgentTurn(context.Context, AgentSession, string, TurnLimits, EventSink) (TurnResult, error)`
  - `type TurnLimits struct { MaxToolCalls int; ToolTimeout time.Duration }`

- [ ] **Step 1: Write failing context and turn tests**

Add:
- `TestKnowledgeDigestHonorsCountAndByteLimits`: 20 reusable Knowledge entries produce at most 8, each statement at most 200 UTF-8 bytes and total at most 2048 bytes.
- `TestKnowledgeDigestOrdersDeterministically`: trust order, `ValidFrom`, then ID decide selection.
- `TestKnowledgeDigestExcludesRefutedExpiredAndWrongScope`: none appear in the prompt or `PlanRequest`.
- `TestPlannerAndExecutorReceiveOnlyBoundedRecentReports`: planner gets at most `MaxActiveBranches` attempt summaries; executor gets no unrelated branch reports.
- `TestRunAgentTurnSettlesOnlyOnAgentSettled`: `agent_end` does not settle; `agent_settled` does.
- `TestRunAgentTurnEnforcesToolLimitAndTimeout`: live tool count above limit and tool timeout both abort the session and return an error.
- `TestTurnEventSinkFailureAborts`: a sink error aborts the active turn and surfaces the persistence error.
- `TestPlannerDefaultUsesReadOnlyRole`: fake runtime receives `AgentRolePlanner`; default strategy rejects shell-style tool use in its prompt contract.
- `TestExecutorReportMustBeRegularBoundedFile`: missing, directory and oversized `report.json` fail the Attempt.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./... -run 'TestKnowledgeDigest|TestPlannerAndExecutor|TestRunAgentTurn|TestTurnEventSink|TestPlannerDefault|TestExecutorReport'`

Expected: FAIL on missing context builders, turn runner and report validation.

- [ ] **Step 3: Implement deterministic bounded context**

Implement the builders listed in Interfaces. Truncation must be valid UTF-8 and must return the number of dropped Knowledge entries. Emit `exploration.context.truncated` with `{context, candidates, kept, dropped}` from the engine after builder calls.

Planner context order:
- task objective and exact Target
- current Branch summaries bounded by `MaxNodes`
- bounded Knowledge digest
- authoritative progress
- last batch Attempt summaries only

Executor context order:
- objective, exact Branch, success/failure conditions, Budget
- bounded task/target Knowledge digest
- report schema and evidence file instructions

- [ ] **Step 4: Implement `RunAgentTurn` and role defaults**

`RunAgentTurn` must:
- call `Prompt`
- consume events until `agent_settled`
- count non-report tool calls and abort above the limit
- enforce per-tool timeout and turn context
- forward every event through `EventSink`
- treat channel close before settlement, malformed event data and runtime errors as failures
- return assistant text/tool names/usage without making any success claim

Default Planner strategy must use only `read` tools in Pi configuration and return a JSON proposal. Default Executor strategy must reset the session, remove stale `report.json`, run the bounded attempt, read a regular file no larger than `MaxReportBytes`, and parse/validate `AttemptReport`.

- [ ] **Step 5: Run focused tests to verify they pass**

Run: `go test ./... -run 'TestKnowledgeDigest|TestPlannerAndExecutor|TestRunAgentTurn|TestTurnEventSink|TestPlannerDefault|TestExecutorReport'`

Expected: PASS with zero failures.

- [ ] **Step 6: Run suite and commit**

Run: `go test ./...`

Expected: PASS.

```bash
git add planner.go executor.go exploration.go ports.go planner_test.go executor_test.go context_test.go
git commit -m "feat(exploration): bound context and define turn protocol"
```

---

### Task 3: Evidence Validation and Content-Addressed Artifacts

**Files:**
- Create: `artifact.go`, `artifact_test.go`
- Modify: `executor.go`, `exploration.go`, `knowledge.go`, `ports.go`
- Modify: `redactor.go`, `redactor_test.go`

**Interfaces:**
- Consumes: `ObservationProposal.EvidenceFiles`, `AttemptRequest.WorkspaceDir`, `ArtifactStore.Put`.
- Produces:
  - `func ArchiveAttemptEvidence(context.Context, ArtifactStore, Redactor, EvidenceArchiveRequest) ([]Observation, error)`
  - `type EvidenceArchiveRequest struct { RunID, BranchID, AttemptID, SessionID, WorkspaceDir string; Observations []ObservationProposal; MaxFiles int; MaxFileBytes, MaxTotalBytes int64 }`
  - `func ValidateEvidencePath(root, candidate string) (string, error)`

- [ ] **Step 1: Write failing evidence tests**

Add `artifact_test.go`:
- `TestValidateEvidencePathRejectsAbsoluteTraversalAndSymlink`: absolute path, `../secret`, symlink escaping workspace and directory each fail.
- `TestArchiveEvidenceRejectsFileLimit`: 17 files with max 16 fails without `ArtifactStore.Put`.
- `TestArchiveEvidenceRejectsOversize`: one file over `MaxArtifactBytes` and aggregate over `MaxAttemptArtifactBytes` fail.
- `TestArchiveEvidenceUsesContentHash`: identical bytes produce the expected SHA-256 Artifact ID.
- `TestArchiveEvidenceRequiresObservationFile`: Observation with no evidence file fails before Knowledge promotion.
- `TestArchiveEvidenceRequiresArtifactWrite`: ArtifactStore failure aborts the Attempt and Run.
- `TestTextArtifactIsRedactedBeforeHash`: token text produces a redacted stored hash and `Redacted=true`.
- `TestBinaryArtifactWithKnownSecretIsRejected`: known token bytes in binary produce no stored Artifact.

Extend `redactor_test.go`:
- `TestRedactJSONPreservesStructure`: secrets in nested JSON strings are removed while JSON remains valid.
- `TestContainsDetectsKnownSecret`: exact known values and secret-like patterns are detected.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./... -run 'TestValidateEvidencePath|TestArchiveEvidence|TestTextArtifact|TestBinaryArtifact|TestRedactJSON|TestContainsDetects'`

Expected: FAIL because evidence validation and artifact persistence do not exist.

- [ ] **Step 3: Implement secure evidence validation and archive**

Implementation rules:
- Clean the candidate relative path and reject absolute paths, empty paths, `..`, non-regular files, symlink escapes and files outside `EvalSymlinks(WorkspaceDir)`.
- Enforce per-file size, per-attempt file count and aggregate bytes before `Put`.
- Read through a limit reader and verify declared/actual size.
- For UTF-8 text, apply Redactor before `ArtifactStore.Put`.
- For binary content, reject if known secret byte sequences appear.
- Use the SHA-256 of stored bytes as Artifact ID.
- Persist Observation only after every referenced Artifact is successfully stored; a failed archive never leaves a successful Observation.

- [ ] **Step 4: Wire evidence into Attempt collection**

In the single-writer collector:
- parse the report
- validate Observation local IDs are unique and non-empty
- archive evidence for each Observation
- record Artifacts and Observations in deterministic report order
- validate Knowledge `ObservationRefs` against archived Observation local-ID mapping
- only then promote Knowledge and child branches

Malformed report behavior:
- planner malformed output fails the Run
- executor malformed output fails only that Attempt and leaves the Branch retryable until its attempt budget is exhausted

- [ ] **Step 5: Run focused tests to verify they pass**

Run: `go test ./... -run 'TestValidateEvidencePath|TestArchiveEvidence|TestTextArtifact|TestBinaryArtifact|TestRedactJSON|TestContainsDetects'`

Expected: PASS.

- [ ] **Step 6: Run suite and commit**

Run: `go test ./...`

Expected: PASS.

```bash
git add artifact.go artifact_test.go executor.go exploration.go knowledge.go ports.go redactor.go redactor_test.go
git commit -m "feat(evidence): archive validated content-addressed artifacts"
```

---

### Task 4: Knowledge Trust, Conflict Review and Cross-Run Reuse

**Files:**
- Modify: `knowledge.go`, `exploration.go`, `planner.go`, `types.go`
- Create: `knowledge_test.go`
- Modify: `context_test.go`

**Interfaces:**
- Consumes: archived Observations, `KnowledgeProposal`, `KnowledgeQuery`, `QueryKnowledge`.
- Produces:
  - `func PromoteKnowledge(KnowledgePromotionRequest) (Knowledge, []KnowledgeEvent, error)`
  - `func ResolveReview(reviewOf []string, proposal []KnowledgeProposal) (winnerID, loserID string, err error)`
  - `func KnowledgeFingerprint(Knowledge) string`
  - `func FilterKnowledge([]Knowledge, KnowledgeQuery) []Knowledge`

- [ ] **Step 1: Write failing knowledge tests**

Add:
- `TestKnowledgeRequiresObservationFromCurrentAttempt`: unknown/local reference from another Attempt is rejected.
- `TestCorroborationRequiresDistinctExecutorSessions`: two Attempts from the same Session cannot corroborate; two distinct Sessions can.
- `TestRefuteCreatesContestedPairAndReviewBranch`: pair becomes contested and one review Branch binds both IDs.
- `TestReviewResolutionChangesOnlyBoundPair`: valid support/refute resolves winner/loser; mismatched pair is rejected.
- `TestReviewInconclusiveLeavesContested`: no trust change without both support and refute.
- `TestRefutedKnowledgeIsNeverInjected`: planner/executor contexts exclude refuted entries.
- `TestCrossRunReuseRespectsTaskTargetScopeAndReuse`: never/instance/stable and task/target/instance cases match spec.
- `TestExpiredKnowledgeIsExcludedAndAudited`: expired entries are absent and produce an expiry event.
- `TestKnowledgeLimitIsMechanical`: proposal above 200 entries is rejected without partial persistence.
- `TestKnowledgeFingerprintRejectsDuplicate`: same normalized topic/scope/kind/target is rejected.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./... -run 'TestKnowledge|TestCorroboration|TestRefute|TestReview|TestCrossRunReuse|TestExpiredKnowledge'`

Expected: FAIL because trust transitions and query filtering are not implemented.

- [ ] **Step 3: Implement code-owned trust transitions**

- Generate IDs from normalized topic, scope, kind, target fingerprint and a Run-scoped uniqueness suffix; never trust agent IDs.
- New entries start `observed`.
- `Supports` appends the current `KnowledgeSource`; at least two distinct Executor `SessionID` values transition to `corroborated`.
- `Refutes` marks both existing entries and the new conflict entry `contested`, adds reciprocal `Contradicts`, and creates exactly one `BranchReview` with `ReviewOf` sorted two IDs.
- `ResolveReview` requires exactly two unique known IDs and a proposal that supports exactly one and refutes the other.
- No code path constructs `KnowledgeVerified`.

- [ ] **Step 4: Implement query and fingerprint semantics**

`FilterKnowledge` must:
- enforce same `TaskID`
- apply `AllowCrossRun`, `ReuseStable`, `ReuseInstance`, `ReuseNever`
- apply task/target/instance scope and target fingerprint
- remove refuted and expired entries
- return deterministic ordering by trust, `ValidFrom` desc and ID asc

`KnowledgeFingerprint` must use canonicalized topic, scope, kind and target fingerprint; address order must not affect it.

- [ ] **Step 5: Run focused tests to verify they pass**

Run: `go test ./... -run 'TestKnowledge|TestCorroboration|TestRefute|TestReview|TestCrossRunReuse|TestExpiredKnowledge'`

Expected: PASS.

- [ ] **Step 6: Run suite and commit**

Run: `go test ./...`

Expected: PASS.

```bash
git add knowledge.go knowledge_test.go exploration.go planner.go types.go context_test.go
git commit -m "feat(knowledge): enforce trust review and scoped reuse"
```

---

### Task 5: SQLite Canonical Store, Artifact Store and Redacting Wrapper

**Files:**
- Modify: `store/sqlite/store.go`
- Create: `store/sqlite/exploration.go`, `store/sqlite/artifact.go`, `store/sqlite/migrations.go`
- Modify: `store/sqlite/store_test.go`, `store/sqlite/exploration_v2_test.go`
- Create: `store/sqlite/exploration_v3_test.go`, `store/sqlite/artifact_test.go`
- Modify: `runner.go`, `redactor.go`
- Create: `redaction_integration_test.go`

**Interfaces:**
- Consumes: public `Store` and `ArtifactStore` contracts.
- Produces:
  - `func Open(path string) (*Store, error)` implementing both `harness.Store` and `harness.ArtifactStore`
  - `func (s *Store) QueryKnowledge(context.Context, harness.KnowledgeQuery) ([]harness.Knowledge, error)`
  - `func (s *Store) Put(context.Context, harness.ArtifactInput) (harness.Artifact, error)`
  - `func (s *Store) Open(ctx context.Context, artifactID string) (io.ReadCloser, error)`

- [ ] **Step 1: Write failing migration and persistence tests**

Add:
- `TestMigrationPreservesAuditRows`: opening a v1/v2 fixture preserves event/log counts and prior payloads.
- `TestMigrationIsIdempotent`: opening twice leaves schema version and row counts stable.
- `TestRunScopedIDsAreUniqueAcrossRuns`: two Runs can each have root plus `branch-1` logical names without primary-key collision.
- `TestKnowledgeVersionsAreAppendOnly`: trust update creates a new version and old source/trust rows remain queryable for audit.
- `TestQueryKnowledgeFiltersAtStoreBoundary`: task, run, target, scope, reuse and expiry filtering agrees with `FilterKnowledge`.
- `TestAppendArtifactMetadataAndBytes`: metadata row and content hash are consistent.
- `TestArtifactOpenRejectsUnknownAndTraversalID`: unknown ID, slash and `..` IDs fail.
- `TestRedactingStoreCoversAllTables`: run a fake successful/failed flow with a known token in every textual field and scan every table for the token.
- `TestStoreWriteFailureStopsRun`: failing each canonical write produces a failed Run rather than continuing.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./... -run 'TestMigrationPreserves|TestMigrationIsIdempotent|TestRunScopedIDs|TestKnowledgeVersions|TestQueryKnowledge|TestAppendArtifact|TestArtifactOpen|TestRedactingStore|TestStoreWriteFailure'`

Expected: FAIL because v3 tables, artifact storage and redacting wrappers do not exist.

- [ ] **Step 3: Implement additive SQLite schema**

Create v3 tables without deleting historical rows:
- `exploration_branches`
- `exploration_attempts`
- `exploration_observations`
- `exploration_observation_artifacts`
- `artifacts`
- `knowledge`
- `knowledge_sources`
- `knowledge_versions`

Use composite uniqueness including `run_id` where runtime identity is Run-scoped. Use `CREATE TABLE IF NOT EXISTS` and additive `ALTER TABLE` only. Keep event/log rows append-only.

Artifact bytes:
- store under `.harness/artifacts/sha256/<first-two>/<hash>` by default
- write via temporary file + fsync + atomic rename
- verify declared size while streaming
- refuse symlink targets and unknown/unsafe IDs
- preserve duplicate content by reusing bytes while recording a new source relation

- [ ] **Step 4: Implement redacting wrappers in Runner composition**

Wrap every RunSpec Store/ArtifactStore once at the Runner boundary. All writes, including normalized states, Errors, Verdict evidence, Knowledge sources, Artifact metadata and outputs, pass through the wrapper. A redaction or persistence error cancels producers and fails the Run.

- [ ] **Step 5: Run focused tests to verify they pass**

Run: `go test ./store/sqlite ./... -run 'TestMigrationPreserves|TestMigrationIsIdempotent|TestRunScopedIDs|TestKnowledgeVersions|TestQueryKnowledge|TestAppendArtifact|TestArtifactOpen|TestRedactingStore|TestStoreWriteFailure'`

Expected: PASS.

- [ ] **Step 6: Run race test and commit**

Run: `go test ./store/sqlite ./... && go test -race ./store/sqlite ./...`

Expected: PASS.

```bash
git add store/sqlite runner.go redactor.go redaction_integration_test.go
git commit -m "feat(store): persist scoped state and content-addressed evidence"
```

---

### Task 6: Pi Runtime Adapter

**Files:**
- Create: `pi/config.go`, `pi/config_test.go`
- Modify: `pi/session.go`, `pi/session_test.go`, `pi/session_reset_test.go`
- Modify: `images/pi-kali/Dockerfile` only if runtime configuration requires a verified image change

**Interfaces:**
- Consumes: `AgentRuntimeFactory`, `RuntimeRequest`, `AgentSession`.
- Produces:
  - `type Config struct { Provider, Model, Thinking, SystemPrompt, ModelsJSON string; Tools []string; ExtraArgs []string; EnvPassthrough []string; Skills []string; Image string }`
  - `func NewFactory(Config) harness.AgentRuntimeFactory`

- [ ] **Step 1: Write failing Pi adapter tests**

Add:
- `TestFactoryPlannerUsesReadOnlyTools`: planner role gets `read` and no `bash`.
- `TestFactoryExecutorUsesConfiguredOrDefaultShellTools`: executor gets configured tools or `read,bash,edit,write`.
- `TestFactoryRejectsInvalidModelsJSON`: invalid JSON fails before process start.
- `TestFactoryNeverReceivesDockerSocket`: `ProcessSpec.Mounts` has no Docker socket.
- `TestPiStrictLFJSONL`: CRLF-only framing is rejected; oversized line reports malformed event without panic.
- `TestAgentEndDoesNotSettle` and `TestAgentSettledSettlesTurn`.
- `TestResetAllowsSecondTurn`: two prompt/reset cycles each observe their own `agent_settled`; no old channel result shortcuts the second turn.
- `TestCloseWaitsForReaderProducers`: close returns only after event/log producers stop.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pi -run 'TestFactory|TestPiStrict|TestAgentEnd|TestAgentSettled|TestResetAllows|TestCloseWaits'`

Expected: FAIL where Pi still exposes core types or does not satisfy the new session contract.

- [ ] **Step 3: Implement runtime-neutral Pi configuration and session**

Move provider/model/tool/image/skills configuration to `pi.Config`. `NewFactory` captures immutable config and returns a factory. `NewSession` uses `RuntimeRequest.Role` to enforce planner vs executor defaults. Process construction must keep strict command allowlisting, environment passthrough, read-only root, no socket and bounded resources.

Session semantics:
- `Prompt`, `Steer`, `Reset`, `Abort` use JSON commands with IDs and LF.
- Every event is forwarded unchanged with a strict schema envelope.
- `agent_settled` marks the current turn settled; `agent_end` is forwarded but not treated as completion.
- session close closes stdin, waits for process, cancels readers, waits for producer goroutines and closes event/log channels exactly once.
- transport errors become terminal agent events plus channel closure, never silent success.

- [ ] **Step 4: Run focused tests to verify they pass**

Run: `go test ./pi -run 'TestFactory|TestPiStrict|TestAgentEnd|TestAgentSettled|TestResetAllows|TestCloseWaits'`

Expected: PASS.

- [ ] **Step 5: Run Pi race tests and commit**

Run: `go test ./pi && go test -race ./pi`

Expected: PASS.

```bash
git add pi images/pi-kali/Dockerfile
git commit -m "refactor(pi): implement runtime-neutral agent sessions"
```

---

### Task 7: Docker, Verifier and TSec Adapters

**Files:**
- Modify: `sandbox/docker/backend.go`, `sandbox/docker/runtime.go`, `sandbox/docker/runtime_test.go`, `sandbox/docker/runtime_integration_test.go`
- Modify: `verify/command.go`, `verify/command_test.go`
- Modify: `tsec/client.go`, `tsec/client_test.go`, `tsec/live_test.go`
- Test: `adapter_contracts_test.go`

**Interfaces:**
- Consumes: `ProcessStarter`, `ResourceCleaner`, `Backend`, `Verifier`.
- Produces: existing concrete adapters updated to v1 ports; no Pi, Workflow or exploration control imports.

- [ ] **Step 1: Write failing adapter tests**

Add:
- `TestDockerSpecUsesHardenedDefaults`: Docker invocation contains read-only root, no socket, `no-new-privileges`, capability drop and CPU/memory/PID limits.
- `TestDockerCleanupRunsAfterProvisionFailure`: partial target resources are cleaned when provisioning fails.
- `TestDockerCloseIsIdempotent`: repeated Close returns no duplicate-resource error.
- `TestCommandVerifierRejectsMissingScript`: missing script is a failed Run verdict path, not success.
- `TestCommandVerifierUsesRedactedEvidence`: verifier output containing a known token is redacted before persistence.
- `TestTSecBackendMapsCompletion`: non-done progress is observation; done/success maps to authoritative Completion.
- `TestTSecTokenNeverPersisted`: a test flow with token-bearing logs and errors leaves no token in Store or Result.
- `TestAdapterPackagesDoNotImportControlLoop`: AST import test rejects imports from server/CLI/Web into adapters and root-to-adapter imports.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./sandbox/docker ./verify ./tsec ./... -run 'TestDockerSpec|TestDockerCleanup|TestDockerClose|TestCommandVerifier|TestTSecBackend|TestTSecToken|TestAdapterPackages'`

Expected: FAIL against old interfaces and insufficient lifecycle cases.

- [ ] **Step 3: Implement Docker and verifier contracts**

Docker starter must map a complete `ProcessSpec` exactly once, reject unsafe image/command arguments, preserve no-socket and hardening defaults, and clean partial resources. Command verifier must execute with bounded context, treat non-zero exit as unsuccessful verdict unless policy says otherwise, redact evidence and return errors separately.

- [ ] **Step 4: Implement TSec Backend mapping**

Keep TSec lifecycle in Go. Backend `Provision`, `Observe` and `Close` must map API progress/flags into `Target` and `Completion` without exposing token values in diagnostics. Flag submission remains an executor skill; start/close remain SDK-owned.

- [ ] **Step 5: Run focused tests to verify they pass**

Run: `go test ./sandbox/docker ./verify ./tsec ./... -run 'TestDockerSpec|TestDockerCleanup|TestDockerClose|TestCommandVerifier|TestTSecBackend|TestTSecToken|TestAdapterPackages'`

Expected: PASS.

- [ ] **Step 6: Run race tests and commit**

Run: `go test -race ./sandbox/docker ./verify ./tsec ./...`

Expected: PASS for unit tests; do not run or claim live Docker/TSec tests unless explicitly available.

```bash
git add sandbox/docker verify tsec adapter_contracts_test.go
git commit -m "refactor(adapters): align runtime backends and verifiers"
```

---

### Task 8: CLI, HTTP/SSE and Web Adapters

**Files:**
- Modify: `internal/cli/config.go`, `internal/cli/cli.go`, `internal/cli/config_test.go`
- Modify: `server/server.go`, `server/server_test.go`, `server/exploration_v2_test.go`
- Modify: `web/src/api/types.ts`, `web/src/utils/explorationTree.ts`, `web/src/utils/explorationTree.test.ts`, `web/src/pages/RunDetailPage.tsx`, relevant component tests
- Modify: `README.md`, `docs/architecture.md`, `docs/pi.md`, `docs/tsec.md`
- Create: `docs/migration-v1.md`

**Interfaces:**
- Consumes: `RunDescriptor`, `Runner`, public Store read methods and v1 event names.
- Produces: CLI/HTTP/Web adapters that construct or display v1 Runs without owning exploration state.

- [ ] **Step 1: Write failing CLI/server/UI tests**

Add:
- `TestCLIComposesPlannerExecutorsAndAdapters`: YAML with one planner, two executors, target isolation and policy maps to the correct v1 composition.
- `TestCLIRejectsWorkflowAndPiSpecKeys`: legacy keys fail with migration guidance.
- `TestServerAcceptsRunDescriptor`: valid descriptor returns Run ID; unsupported isolation and unknown fields fail.
- `TestServerRejectsFunctionBearingFields`: interfaces cannot be supplied over HTTP.
- `TestSSEReplaysAfterSequence`: reconnect with `Last-Event-ID`/`after_seq` returns only newer events.
- `TestWebExplorationReducerHandlesV1Events`: root/hypothesis/review branches, retries, rejection, artifacts and knowledge versions render deterministically.
- `TestWebIgnoresUnknownEventWithoutDroppingRun`: unknown event is skipped and other state remains intact.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli ./server && npm --prefix web run test -- explorationTree`

Expected: FAIL because descriptors and event names still target the legacy model.

- [ ] **Step 3: Implement CLI composition and migration errors**

CLI parses only serializable `RunDescriptor` fields. A deployment composition function maps runtime/backend/verifier/artifact-store configuration to interfaces. Legacy `workflow`, `pi`, `agents`, `targets` and `exploration.configure` keys fail with messages pointing to `docs/migration-v1.md`.

- [ ] **Step 4: Implement server descriptor behavior and event replay**

Server uses public Runner/Store contracts only. It enforces strict JSON decoding, caller-managed target isolation, local single-user defaults and explicit cancellation/steer endpoints. SSE remains seq-based and read-only Web never creates verdicts.

- [ ] **Step 5: Update Web reducers and pages**

Map v1 events:
- `run.*`
- `exploration.branch.*`
- `exploration.attempt.*`
- `exploration.observation.*`
- `artifact.*`
- `knowledge.*`
- `verification.*`

Display branch kind/status, attempt attempts, artifact metadata, knowledge trust/version and authoritative verdict separately. Unknown events are ignored per event.

- [ ] **Step 6: Update user docs and migration guide**

README and architecture docs must state:
- single Run/single Target
- caller-managed isolation, not SDK network ACLs
- Pi/Docker defaults and replacement interfaces
- no Workflow, no recovery, no verified knowledge
- Artifact evidence lifecycle
- migration table and old-to-new Go examples

`docs/migration-v1.md` must include descriptors, custom runtime, custom verifier, CLI/HTTP config, SQLite upgrade and secret-handling notes.

- [ ] **Step 7: Run focused tests to verify they pass**

Run:
```bash
go test ./internal/cli ./server ./...
npm --prefix web run test
npm --prefix web run typecheck
npm --prefix web run lint
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/cli server web README.md docs/architecture.md docs/pi.md docs/tsec.md docs/migration-v1.md
git commit -m "refactor(surfaces): expose the v1 agent sdk"
```

---

### Task 9: Legacy Removal, End-to-End Acceptance and Final Verification

**Files:**
- Delete: any remaining legacy Workflow, direct-workflow, v1/v2 exploration or deprecated bridge files
- Modify: `docs/superpowers/specs/2026-10-21-minimal-harness-architecture-design.md` status
- Modify: `docs/superpowers/specs/2026-10-21-exploration-convergence-design.md` status
- Modify: `examples/run.yaml`, `examples/tsec.yaml`, `examples/verify.sh`
- Test: `acceptance_test.go`, `import_boundary_test.go`

**Interfaces:**
- Consumes: all prior task outputs.
- Produces: one production control path and the final acceptance matrix.

- [ ] **Step 1: Write final failing acceptance and boundary tests**

Add `acceptance_test.go`:
- `TestEndToEndPlannerExecutorEvidenceKnowledgeVerdict`: fake Pi runtime + real engine + SQLite + fake Docker process starter + verifier completes a Run; Result contains one verdict, >=2 branches, attempts, observations, artifacts and knowledge; only verifier success sets `StatusSucceeded`.
- `TestMalformedExecutorReportRetriesThenBlocks`: malformed report consumes one attempt and retries; after max attempts branch becomes blocked and Run stagnates without success.
- `TestBackendSuccessRacesInflightAttempt`: backend completion cancels a blocked turn, closes sessions and returns backend verdict.
- `TestSQLiteContainsNoKnownSecrets`: scan every table and artifact byte store and return zero token matches.
- `TestNoDeclaredUnreachableState`: reflect over public status/outcome/trust enums and assert every value is produced by at least one fixture transition.
- `TestRootPackageImportsStdlibOnly`: AST import test rejects adapter/CLI/server imports from root source files.
- `TestNoLegacyControlSymbols`: AST scan rejects `Workflow`, `WorkflowRuntime`, `PiSpec`, `KnowledgeVerified` and old exploration controller symbols in non-comment production code.

Add `import_boundary_test.go`:
- root package imports only standard library
- `pi` imports no `server`, `internal/cli`, `webui` or TSec
- adapters do not import Web
- `internal/cli` and `server` may import adapters only in composition files

- [ ] **Step 2: Run acceptance tests to verify they fail**

Run: `go test ./... -run 'TestEndToEnd|TestMalformedExecutor|TestBackendSuccess|TestSQLiteContainsNoKnownSecrets|TestNoDeclaredUnreachableState|TestRootPackageImports|TestNoLegacyControlSymbols'`

Expected: FAIL until any remaining bridges and acceptance gaps are removed.

- [ ] **Step 3: Remove dead control paths and update document status**

- Remove all old workflow/direct-workflow implementations and aliases.
- Remove references to `PiSpec`, `agent_end` as completion, `verified` knowledge and optional exploration configuration.
- Mark the minimal-harness and convergence specs as superseded by the v1 spec; do not delete their historical content.
- Rewrite examples to the v1 `RunDescriptor` and composition API.
- Ensure every public status/outcome/trust enum has a reachable constructor and acceptance fixture.

- [ ] **Step 4: Run final Go and frontend verification**

Run each command and read its complete output:
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

Expected: every command exits 0. If Docker or live TSec checks are skipped, state that explicitly and do not claim they passed.

- [ ] **Step 5: Commit final cleanup**

```bash
git add -A
git commit -m "refactor(sdk): finalize first-principles agent architecture"
```

- [ ] **Step 6: Perform post-commit verification and delivery**

Run:
```bash
git status --short --branch
git log -1 --oneline
git diff HEAD^ --check
```

Expected: clean working tree and the final commit present. Push the current branch to its configured upstream. If push fails, report the exact error and true remote state.

---

## Execution Notes

- Do not start Task 3 before Task 1's evidence hooks and Task 2's report parsing exist.
- Do not start Task 4 before Task 3 can persist Artifacts and Observations.
- Task 5 may proceed after Task 4's interfaces compile; SQLite is tested behind public ports.
- Task 6 may proceed in parallel with Task 5 only if Pi uses fake Store/ArtifactStore and does not edit shared files.
- Task 7 depends on Tasks 5 and 6.
- Task 8 depends on all public contracts and event schemas being frozen.
- Task 9 is the only place that may delete historical control code and mark superseded specs.
- When a spec and plan disagree, the design spec is binding; record the resolution in the execution ledger and update this plan before continuing.
