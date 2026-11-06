# Repository Guidelines

## Status and Direction

This is a standalone Go SDK for the Red Harness execution and trust boundary.
It does not depend on Python or the legacy `red-copilot` runtime.

Claim/Intent v3 is the current implementation on `main`. The released
`main@0bbee5e` single-session exploration path is historical context only and
must not be reintroduced or extended.

## Current Project Structure

- `core/`: pure domain contracts, policy, validators and typed events.
- `engine/`: single-writer Run lifecycle, Claim/Intent board, one Executor slot,
  budgets, authority arbitration and persistence ports.
- Root `harness`: thin façade over common `core`/`engine` entry points.
- `pi/`: Pi RPC session implementation and hardened command construction.
- `sandbox/docker/`: Docker process starter, external/image/Compose target backends.
- `tsec/`: TSec API client, challenge backend and completion polling.
- `verify/`: external command verifier.
- `store/sqlite/`: canonical SQLite event/log/result persistence.
- `server/`: asynchronous HTTP API, bounded logs and SSE replay.
- `internal/cli/`, `cmd/harness/`: operational CLI and composition roots.
- `web/`, `webui/`: React/Vite observability console and its embedded Go server.
- `skills/tsec-submit/`: embedded Pi skill and authoritative API reference.
- `skills/tools/`: embedded security-tool selection skill with one reference
  per preinstalled command.
- `images/pi-kali/`: pinned Kali + Pi image.

## Current Architecture

The Claim/Intent v3 architecture is defined by the code:

- `core/`: pure domain contracts, policy, validators and typed events; standard
  library only.
- `engine/`: single-writer Run lifecycle, Claim/Intent board, one Executor
  slot, budgets, authority arbitration and persistence ports; imports only
  `core` and the standard library.
- Root `harness`: thin façade over common `core`/`engine` entry points.
- Adapters: `pi`, `sandbox/docker`, `tsec`, `verify`, `store/sqlite`.
- Surfaces: `server`, `internal/cli`, `webui` and `web`.

The current implementation has one writable exploration path. Every Run uses a
serialized Reasoner and exactly one Executor. Branch/Knowledge are removed and must not regain a writable control path.

## Build and Test

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/harness
```

Frontend checks:

```bash
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
```

Docker integration and live TSec/VPN checks are environment-dependent; do not
present them as passing unless they were actually run:

```bash
HARNESS_DOCKER_TESTS=1 go test ./sandbox/docker
HARNESS_TSEC_LIVE=1 BENCHMARK_BASE_URL=... BENCHMARK_TOKEN=... go test ./tsec
```

Live TSec checks require the benchmark VPN and a stopped, unfinished challenge;
they may allocate a remote challenge instance. Never print or commit the token.
For documentation-only changes, at minimum run `git diff --check` and verify
links, commit references and code symbols; do not claim the full Go/Web matrix
was run.

## Engineering Rules

- Keep the public library usable without the CLI or HTTP server.
- Never let agent messages, tool exits or benchmark prose establish success.
  Only a `Verifier` or backend terminal completion may produce a successful
  verdict. Claims, Reason completion, goal candidates and `agent_settled` are
  observations, not success.
- Preserve the v3 trust invariants:
  model output is a proposal; `Artifact -> Observation -> Claim` provenance is
  mandatory for findings; the engine loop is the only board writer; canonical
  commits are atomic; stale board revisions and malformed reports fail closed.
- Do not introduce a second writable exploration path. Do not reintroduce
  Branch/Knowledge writes. They are removed; all new records use Claim/Intent.
- Pi RPC uses strict LF JSONL framing and `agent_settled` for completion.
  `agent_end` is not completion.
- SQLite is the canonical audit store. Keep events/logs append-only. Board
  records, Intent/Attempt history and their events commit atomically; terminal
  Run, Verdict and `run.finalized` finalize atomically.
- The approved cutover is backup-then-rebuild: verify a SHA-256/row-count backup
  manifest before creating a fresh v3 database. Never upgrade the production
  database in place as if old Runs were v3 history. Rollback restores the
  verified backup; the new schema is a one-way boundary.
- Redact known secret values before persisting events, logs, Specs, Results,
  Verdicts and diagnostics. In v3, text evidence is redacted before hashing and
  binary evidence containing a known secret is rejected.
- Containers must not receive the Docker socket. Keep read-only root filesystem,
  `no-new-privileges`, capability dropping and resource limits.
- TSec `BENCHMARK_TOKEN` is an explicit product exception and must never appear
  in SQLite plaintext or diagnostics. It remains readable inside the agent
  container; treat that container as inside the credential trust boundary.
- Keep target authorization separate from runtime binding. A provisioner may
  create addresses and container details, but must not change the authorized
  Target identity. Target reference checks are not network ACLs; callers own
  egress isolation.
- Add tests first; include negative/fail-closed cases for malformed input,
  stale revisions, evidence escape, cancellation, process exit, persistence
  failure, verification errors and cleanup.
- Use Conventional Commits with a scope, for example `feat(pi): ...`.

## Git Workflow

The agent owns the branch lifecycle end to end. Do not stop to ask for
confirmation between these steps; stop only on a real failure and report the
exact command and its output.

### 1. Verification gate

Run the checks this task actually exercises before every commit. A commit that
has not passed them is not committed.

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/harness
git diff --check
```

Run the `web/` matrix (`npm --prefix web run lint|typecheck|test|build`) when
the change touches `web/` or `webui/`. Docker and live TSec checks stay opt-in
as described above. For documentation-only changes the minimum is
`git diff --check` plus verifying links, commit references and code symbols.

If a required check fails, do not commit, do not push and do not merge. Report
the failing command and its output. Never describe a check as passing unless it
ran in this session, and never weaken or skip a check to get a green result.

This local gate — not the PR's CI run — is what authorizes the merge in step 3.

### 2. Branch, commit and push

- Work on a topic branch, never directly on `main`. Branch from an
  up-to-date `main` as `<type>/<short-topic>`, for example `fix/pi-framing`.
- Commit only the files changed for the task; never sweep unrelated worktree
  changes into a commit. Use `git add <path>` with explicit paths.
- Push the branch with `git push -u origin <branch>` as soon as the task's
  verification passes, without asking for confirmation.
- If the push is blocked (auth, non-fast-forward, hook rejection), stop and
  report the exact reason.

### 3. Merge

- Open a pull request with `gh pr create` once the branch is pushed: a
  Conventional-Commits title and a body covering what changed, why, and which
  checks actually ran.
- Merge immediately after push with `gh pr merge <n> --squash --delete-branch`.
  Do not wait for or block on the PR's CI run.
- The local verification gate in step 1 is the merge gate. Never merge a branch
  whose required checks failed locally, and never force-push `main`.
- Do not merge while `main` and the branch conflict; rebase onto the current
  `main` locally, re-run the gate, then push and merge.
- If a change is risky or genuinely ambiguous, leave the PR open and report it
  instead of merging.
- Report the CI result when it arrives, but treat it as post-merge information:
  if it fails, fix forward on a new branch rather than reverting the merge.

### 3a. Do not stall waiting on CI

- Never poll `gh pr checks` or `gh run list` to decide whether to merge.
- Never present a pending or unknown CI state as a reason to wait or to defer
  the merge.

### 4. Branch and worktree cleanup

- After a successful merge, delete the local branch
  (`git branch -d <branch>`), prune remote-tracking refs with
  `git fetch --prune`, and remove any worktree created for the task with
  `git worktree remove <path>`.
- Do not delete a branch whose PR is unmerged, and do not remove a worktree
  that holds uncommitted work; report it instead.

### 5. Keeping `main` current

- Keep the local `main` fast-forwarded to `origin/main`
  (`git fetch origin && git checkout main && git pull --ff-only`) before
  starting new work.
- `git fetch --prune` also drops remote-tracking branches that were already
  deleted upstream; delete the matching local branches once their work is
  merged.
- Never resolve a diverged `main` with a merge or rebase plus force-push;
  report the divergence instead.

### 6. Tags and releases

- Tag only a merged `main` commit whose local verification gate passed, using
  annotated tags (`vMAJOR.MINOR.PATCH`) and Conventional-Commits semantics: a
  breaking change (`!` or `BREAKING CHANGE:`) bumps MAJOR, `feat` bumps MINOR,
  and fixes and chores bump PATCH.
- Push the tag (`git push origin vX.Y.Z`) only after the commit is on
  `origin/main`; the tag push is what triggers the release workflow.
- Do not tag a pre-release, a feature branch or an unreleased schema boundary
  without saying so explicitly in the tag and release notes.
- Never rewrite or delete an already-pushed tag; publish a new patch instead.
