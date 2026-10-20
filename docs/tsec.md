# TSec Benchmark

TSec uses the documented `/openapi/v1/challenges/*` API:

- `GET /openapi/v1/challenges`
- `POST /openapi/v1/challenges/start`
- `GET /openapi/v1/challenges/hint`
- `POST /openapi/v1/challenges/submit`
- `POST /openapi/v1/challenges/close`

The Go client sends `BENCHMARK_TOKEN`, enforces the platform limit of three
active instances and a three-request-per-second budget, and maps platform error
codes into typed errors.

The SDK owns `start`, `close` and completion polling. The embedded
`skills/tsec-submit/SKILL.md` tells Pi how to submit a found flag directly. Pi
does not parse or extract flags from agent messages, and the harness does not
trust Pi's claim that a submission succeeded: completion is confirmed by the
platform challenge state.

`BENCHMARK_TOKEN` is intentionally available inside the agent container for the
skill's `curl` call. The SDK redacts known secret values before writing events,
logs or results to SQLite. The token is never included in the SQLite run spec.

VPN preflight probes the default healthcheck before challenge lifecycle calls.
The CLI exposes `BENCHMARK_BASE_URL` and `BENCHMARK_TOKEN` environment variables
and supports `--base-url`, `--token`, and environment-name overrides.
