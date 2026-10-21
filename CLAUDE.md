# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repository is

Red Harness v1 is a pure Go SDK for the execution and trust boundary of one autonomous security Run against one logical Target (`module github.com/red-copilot/harness`, Go 1.24). It is not a generic agent framework: it fixes one auditable control path (no second Workflow path, no checkpoint/resume) and keeps adapters outside the core.

The root `harness` package must import only the standard library. `import_boundary_test.go` also prevents adapter packages (`pi`, `sandbox/docker`, `tsec`, `verify`, `store/sqlite`) from importing `server`, `internal/cli`, `webui` or the removed `exploration` package, and rejects legacy symbols (`Workflow`, `PiSpec`, `KnowledgeVerified`, ...). Breaking these tests is a design violation, not a test tweak.

Non-negotiable invariants:

- Only `Verifier.Verify` or a `Backend` completion with `Done && Success` can produce a successful run. Agent text, tool exit codes, observations, knowledge trust and `local_success` never establish success.
- Budgets, tree shape, branch identity, attempts, tool calls and context size are enforced in Go, not by prompts.
- Evidence precedes knowledge: Observations reference archived Artifacts; Knowledge references current-Attempt Observations.
- Canonical persistence failure terminates the Run. Never downgrade a store/event/log failure to a retryable report error.
- Known secrets are redacted before every persistence path. Binary evidence containing a known secret is rejected. `BENCHMARK_TOKEN` may enter the agent container but must never reach SQLite, artifacts, logs, errors or results in plaintext.
- Containers never receive the Docker socket and stay read-only-root, `no-new-privileges`, cap-dropped and resource-limited.

## Commands

```bash
go test ./...                    # unit + acceptance; Docker/TSec live tests skip by default
go test -race ./...
go vet ./...
go build ./cmd/harness           # produces ./harness
```

Run one test or package:

```bash
go test -run '^TestEndToEndPlannerExecutorEvidenceKnowledgeVerdict$' -v .
go test -v ./pi -run 'TestBuildCommand'
```

Environment-gated tests:

```bash
HARNESS_DOCKER_TESTS=1 go test ./sandbox/docker   # needs Docker and a built harness/pi-kali:local image
HARNESS_TSEC_LIVE=1 BENCHMARK_BASE_URL=... BENCHMARK_TOKEN=... go test ./tsec
```

Frontend (source in `web/`, Vite outputs into `webui/dist/` which is embedded into the Go binary):

```bash
npm --prefix web ci
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run test        # vitest
npm --prefix web run test:e2e    # Playwright; needs a running server
npm --prefix web run build       # run before `go build ./cmd/harness` when UI changed
```

CLI (the binary auto-loads `.env` from the current directory, without overriding existing env vars):

```bash
harness doctor
harness run --config examples/run.yaml [--json]
harness serve --config examples/run.yaml --listen 127.0.0.1:8080
harness runs show|events --id <run-id> [--after <seq>]
```

Docker integration and live TSec/VPN checks are environment-dependent. Do not report them as passing unless they actually ran.

## Architecture

Four planes, all wired by `Runner`:

- Control plane — `runner.go` (lifecycle, recovery, status transitions), `run.go` (live handle/event fan-out), `exploration.go` (round-barrier scheduler and merge).
- Execution plane — `ports.go` contracts (`AgentRuntimeFactory`, `ProcessStarter`, `Backend`), `planner.go`/`executor.go` (deterministic bounded prompts and turn protocol), `pi/`, `sandbox/docker/`.
- Evidence plane — `artifact.go` (workspace path validation, content-addressed evidence), `knowledge.go` (trust transitions and reuse filtering).
- Trust plane — `store/sqlite/`, `redactor.go`, `verify/`, and the final `Verdict`.

Run flow:

1. `Runner.Start` calls `ValidateRunSpec` (requires task id/objective, target id, caller-managed isolation, planner runtime, ≥1 uniquely named executor runtime, artifact store, and a verifier or backend as authority).
2. `execute` provisions the optional backend, starts `explorationEngine.run`, polls backend completion concurrently, then chooses authority: backend completion first, cancellation/deadline/stagnation next, then `Verifier.Verify`.
3. Each round: planner proposes root-level branches → ready branches are selected by priority/depth/ID → at most `MaxActiveBranches` attempts run concurrently (round barrier, executor sessions reused by index) → the single collector merges results deterministically.
4. Attempt merge: validate `report.json` → archive evidence (all artifact writes first) → create Observations → promote Knowledge → create child branches only for a completed `local_success` attempt with ≥1 observation (capped) → update branch status.

Planner and executor are the same `AgentSession` contract with role-specific tools. Executor output is only `/workspace/report.json`; a missing/malformed report fails that attempt closed. `RunAgentTurn` counts tool calls, enforces per-tool timeouts, aborts on limit/cancellation, and completes only on `agent_settled` — `agent_end` is forwarded but is not completion.

## Package map and responsibilities

- `types_*.go` — public records split by plane (`run`, `target`, `agent`, `exploration`, `knowledge`, `evidence`) plus defaults (`DefaultExplorationPolicy`, `NormalizeExplorationPolicy`). `ports.go` — all interfaces; the root package depends on nothing else.
- `exploration.go` — tree/budget enforcement, child/review branch rules, context truncation events, stagnation detection. Review branches bind exactly two conflicting Knowledge IDs.
- `knowledge.go` — promotion, support/corroboration, refute→`contested`, fingerprint dedupe, `FilterKnowledge` cross-run/reuse/expiry rules. Highest trust is `corroborated`; `refuted` and expired knowledge are never injected.
- `artifact.go` — `ValidateEvidencePath` rejects absolute paths, `..`, symlink escape and non-regular files; text evidence is redacted before hashing; `ArtifactStoreError` marks canonical failures that must end the Run.
- `redactor.go` — secret values from `RunSpec.SecretEnv`, agent env and `EnvFrom`; also redacts flag-like patterns. Apply it to every new persistence path.
- `pi/` — Pi RPC adapter. Strict LF JSONL; response correlation by ID; malformed framing/process exit fails the turn; `Reset` sends `new_session`; `BuildCommand` is the hardened fixed base (no MCP/extensions/approval/context files) with role tools.
- `sandbox/docker/` — `BuildRunArgsChecked` fails closed on malformed specs; secrets pass as env *names* in argv, values via process env; labels `harness.managed` drive orphan cleanup. Backends: `ExternalBackend`, `ImageBackend`, `ComposeBackend`.
- `tsec/` — challenge API client and `Backend`. `Observe` returns an authoritative `Done/Success` only when the challenge reports completed; rate limits/duplicate instances fail closed. Flag submission remains an executor skill.
- `verify/` — external command verifier: one JSON object on stdin, a `Verdict` JSON object on stdout, non-zero exit is an error.
- `store/sqlite/` — canonical audit store (schema v3, WAL). Events/logs have monotonic sequences for SSE; artifacts are content-addressed under `.harness/artifacts/sha256/`; knowledge versions are append-only. Opening a DB performs additive migration and `NewRunner` marks unfinished runs `interrupted` unless `SkipRecovery`.
- `server/` — asynchronous HTTP API: `POST /v1/runs` (descriptor, function fields cannot be supplied), run/events SSE/logs/verdict/branches/attempts/observations/artifacts, cancel/steer, task knowledge.
- `internal/cli/`, `cmd/harness/` — CLI and config composition. `harness/v1` YAML/JSON only; legacy keys (`workflow`, `pi`, `agents`, `targets`, `exploration`) fail closed per `docs/migration-v1.md`. Target kinds: `external`, `image`, `compose`, `tsec`; agent runtimes are always composed as Pi via `AgentSpec.RuntimeConfig`.
- `webui/` — embeds `webui/dist` and serves the SPA, delegating `/v1` and `/healthz` to `server/`.
- `skills/` — embedded Pi skills (`tsec-submit`, `tools`); the tool skill has one bounded reference per preinstalled command. `embedded.go` materializes them into the run's skills directory.

## Working conventions

- Add tests first, including fail-closed cases for malformed input, cancellation, process exit, verification errors and cleanup. `acceptance_test.go` exercises the full planner→executor→evidence→knowledge→verdict path with fake strategies.
- Keep persistence writes append-only or versioned; do not reintroduce JSONL files as the primary run record.
- Use Conventional Commits with a scope (e.g. `feat(pi): ...`, `fix(exploration): ...`).
- Per `AGENTS.md`: after a task is implemented and its required verification passes, commit only that task's changed files and push the current branch to its upstream without asking. If verification fails or the push is blocked, stop and report the exact reason.
- More detail: `README.md`, `docs/architecture.md`, `docs/pi.md`, `docs/tsec.md`, `docs/migration-v1.md`.
