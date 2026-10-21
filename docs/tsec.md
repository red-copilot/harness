# TSec Backend Adapter

TSec is a `Backend`, not a core concept. The SDK owns challenge start, close and completion polling. Flag submission remains an Executor skill.

## Lifecycle

- `Provision` performs optional VPN preflight, starts the challenge and maps addresses plus bounded metadata into `Target`.
- `Observe` maps partial flag progress to a non-authoritative Observation and completed challenge state to an authoritative `Completion`.
- `Close` releases the challenge instance.

Only a completed TSec challenge can establish backend success. Agent claims and submitted flags are not sufficient.

## Credentials

`BENCHMARK_TOKEN` is an explicit product exception: it may be passed into the agent container for the embedded submission skill. It must never reach SQLite plaintext, Artifact bytes, logs, errors, Result or Verdict. The Runner redacts known secret values on every persistence path.

The container itself is part of the credential trust boundary. Callers must not expose it beyond the authorized challenge environment.

## API

The client uses:

- `GET /openapi/v1/challenges`
- `POST /openapi/v1/challenges/start`
- `GET /openapi/v1/challenges/hint`
- `POST /openapi/v1/challenges/submit`
- `POST /openapi/v1/challenges/close`

Rate limits, duplicate instances and capacity errors fail closed. Live TSec/VPN tests are environment-dependent and are not part of the default unit suite.
