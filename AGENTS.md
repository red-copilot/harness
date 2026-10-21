# Repository Guidelines

## Project Structure

This is a standalone Go SDK for the Red Harness execution/trust boundary. It
does not depend on Python or the legacy `red-copilot` runtime.

- `types_*.go`, `runner.go`, `run.go`: public harness contracts by plane and the run lifecycle.
- `pi/`: Pi RPC session implementation and hardened command construction.
- `sandbox/docker/`: Docker process starter, external/image/Compose target backends.
- `tsec/`: TSec API client, challenge backend and completion polling.
- `verify/`: external command verifier.
- `store/sqlite/`: canonical SQLite event/log/result persistence.
- `server/`: asynchronous HTTP API and SSE replay.
- `internal/cli/`, `cmd/harness/`: operational CLI.
- `skills/tsec-submit/`: embedded Pi skill and authoritative API reference.
- `skills/tools/`: embedded security-tool selection skill with one reference
  per preinstalled command.
- `images/pi-kali/`: pinned Kali + Pi image.
- `docs/`: architecture, Pi and TSec contracts.

## Build and Test

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/harness
```

Docker integration and live TSec/VPN checks are environment-dependent; do not
present them as passing unless they were actually run.

## Engineering Rules

- Keep the public library usable without the CLI or HTTP server.
- Never let agent messages, tool exits or benchmark prose establish success.
  Only a `Verifier` or backend completion may produce a successful verdict.
- Pi RPC uses strict LF JSONL framing and `agent_settled` for completion.
  `agent_end` is not completion.
- SQLite is the canonical audit store. Do not reintroduce JSONL files as the
  primary run record.
- Redact known secret values before persisting events, logs and results.
- Containers must not receive the Docker socket. Keep read-only root filesystem,
  `no-new-privileges`, capability dropping and resource limits.
- TSec `BENCHMARK_TOKEN` is an explicit product exception and must never appear
  in SQLite plaintext or diagnostics.
- Add tests first; include negative/fail-closed cases for malformed input,
  cancellation, process exit, verification errors and cleanup.
- Use Conventional Commits with a scope, for example `feat(pi): ...`.
- After a task is implemented and its required verification passes, commit
  only the files changed for that task and push the current branch to its
  upstream automatically, without asking for additional confirmation. If
  verification fails or the push is blocked, stop and report the exact reason.
