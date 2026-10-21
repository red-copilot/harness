# Minimal Harness Architecture

- Date: 2026-10-21
- Status: Approved in conversation
- Change type: Internal refactor with intentional v1alpha1 Go API changes

## Intent

Red Harness is an execution and trust boundary for untrusted security agents. It is
not an agent reasoning framework. The architecture must make that boundary small,
explicit, and mechanically enforced.

The refactor keeps the existing library/CLI/HTTP/Web behavior while reducing the
core to five responsibilities:

1. Run lifecycle and cancellation.
2. Target provisioning through a `Backend`.
3. Isolated agent-session lifecycle through `SessionFactory`.
4. Canonical audit persistence through `Store`.
5. Authoritative verdict selection through `Verifier` or backend completion.

Docker, Pi, SQLite, TSec, CLI, HTTP, and the Web console are adapters. Exploration,
knowledge promotion, branch scheduling, and multi-role orchestration are an
optional workflow outside the core package.

## Irreducible invariants

- **Verdict authority:** no agent message, tool result, workflow event, session
  status, or knowledge item can construct a successful verdict.
- **Bounded lifecycle:** every opened session, target backend, and log producer is
  closed on success, failure, cancellation, timeout, and panic-free early return.
- **Canonical audit:** every workflow/session event is persisted through the core
  runtime after redaction; a rejected event write stops the run.
- **Secret boundary:** the core owns redaction and applies it before event, log,
  normalized exploration-state, result, verdict, and run-record persistence.
- **Single control owner:** `Runner` owns the run event loop, backend observation,
  budget enforcement, verdict decision, and finalization. A `Workflow` may only
  orchestrate work through the runtime it is given.
- **Narrow extension:** a workflow can open sessions, emit events, read the
  target/task/budget/progress, and return a result. It cannot access the verifier,
  backend, store, or finalize a run directly.

## Core model

`RunSpec` remains the input contract and adds:

```go
type Workflow interface {
    Execute(context.Context, WorkflowRuntime) (WorkflowResult, error)
}

type WorkflowResult struct {
    Stagnated bool
}

type WorkflowRuntime interface {
    RunID() string
    Task() Task
    Target() Target
    Agents() []AgentSpec
    Budget() Budget
    WorkspaceRoot() string
    Progress() Completion
    OpenSession(context.Context, AgentSpec) (SessionLease, error)
    Emit(context.Context, Event) error
    Redact(string) string
}

type SessionLease struct {
    ID           string
    Agent        AgentSpec
    WorkspaceDir string
    Session      AgentSession
}
```

`RunSpec.Workflow` is runtime-only and omitted from serialization. A nil workflow
selects the built-in direct workflow: start each declared `AgentSpec`, send the
standard bounded task prompt, persist its events/logs, and wait for settlement.
`RunSpec.WorkflowConfig` is an optional serializable audit descriptor with a
workflow name and raw configuration; the core never interprets it.

`Runner` remains the only run controller. It provisions the target, prepares the
workspace and embedded skills, constructs the runtime, executes the workflow,
observes backend completion, enforces budgets, closes every session, invokes the
verifier when appropriate, persists the verdict/result, and finishes the run.

`WorkflowRuntime.OpenSession` is the only session-creation path. It enforces the
configured maximum, unique session IDs, and creates the isolated session workspace.
`WorkflowRuntime.Emit` fills run identity/time, applies redaction, persists the
event, publishes it to live subscribers, and returns persistence errors to the
caller. Log persistence failures also terminate the run.

## Optional exploration workflow

The current v2 binary-tree exploration implementation moves to package
`github.com/red-copilot/harness/exploration`. Its public policy, branch, attempt,
observation, and knowledge types live there. Root `harness` no longer imports or
defines exploration control semantics.

`exploration.Configure(spec, policy, store)` normalizes the policy, installs the
workflow, and records a generic `WorkflowConfig{Name: "exploration"}` for audit.
The CLI default remains unchanged for single-agent configs: absence of an explicit
policy enables the exploration workflow with default bounds.

The exploration workflow:

- resolves planner/executor roles from `RunSpec.Agents`;
- opens sessions lazily through `WorkflowRuntime.OpenSession`;
- adapts sessions to the existing planner/executor contracts;
- emits `exploration.*` events through `WorkflowRuntime.Emit`;
- propagates event and normalized-store failures as terminal workflow errors;
- returns `WorkflowResult{Stagnated: true}` when the tree exhausts progress.

It never waits for backend success or invokes a verifier; the core does both.

## Adapter changes

- `pi`, `sandbox/docker`, `tsec`, and `verify` retain their current contracts.
- `store/sqlite` implements exploration persistence against the new package types.
- CLI config still accepts the same YAML `exploration:` block and attaches the
  exploration workflow only after opening the store.
- Server requests accept an optional top-level `exploration` policy and pass it to
  the builder; run/event/log/verdict response shapes stay unchanged.
- The Web console continues to consume the same `exploration.*` event payloads.
- README, architecture docs, and examples describe one execution engine and one
  optional exploration workflow.

## Removed v1 control path

Delete the production-unreachable Controller/FactLedger/Frontier/Explorer/Planner
implementation and tests. Keep direct operator steering through `Run.Steer`, but
remove synthetic `exploration.state.updated` / `exploration.frontier.updated`
events from the direct path. Those event names belong only to the optional
exploration workflow.

## Failure behavior

- Workflow errors fail the run unless an authoritative backend verdict already won.
- A backend completion cancels the workflow and wins the run with its verdict.
- A verifier runs only after the workflow and all sessions have finalized.
- Context deadline, cancellation, budget exhaustion, workflow stagnation, and
  missing authoritative verdict map to their existing run statuses.
- Any event/log/run-record/verdict persistence error produces a failed result;
  the implementation must not report a successful run after a failed audit write.
- All sessions are aborted and closed before backend cleanup and final result.

## Non-goals

- No new agent roles, UI features, retrieval strategy, or model transport.
- No generalized plugin discovery mechanism; workflows are injected in Go.
- No relaxation of container hardening or verdict authority.
- No backward-compatible re-export of the removed exploration-control API from the
  root package.

## Acceptance

- A custom workflow can run through the core without importing CLI/server/Pi.
- The default workflow still runs existing direct-session tests.
- Exploration continues to work through `go test ./...` after moving packages.
- Event persistence failure stops the run and cannot yield success.
- Backend completion preempts exploration and cancels sessions.
- `go test ./...`, `go test -race ./...`, `go vet ./...`,
  `go build ./cmd/harness`, frontend lint/typecheck/test/build, and
  `git diff --check` all pass.
