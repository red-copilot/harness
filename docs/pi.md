# Pi Runtime Adapter

Pi is the default `AgentRuntimeFactory`. It is not part of the root contract.

```go
runtime := pi.NewFactory(pi.Config{
    Provider: "openai",
    Model:    "gpt-5",
    Thinking: "high",
})
```

`AgentSpec.RuntimeConfig` accepts the same JSON fields and overrides the factory config per role.

## RPC

```text
pi --mode rpc --no-session --no-approve --no-context-files \
   --no-extensions --no-mcp --no-prompt-templates --no-themes \
   --no-skills --tools <role tools> --offline
```

- Framing is strict LF-delimited JSONL.
- Commands use unique IDs and responses are correlated by ID.
- `agent_settled` is turn completion; `agent_end` is forwarded but never completes a turn.
- Pre-settlement channel close, malformed framing, command errors and process exit fail the turn closed.
- `Reset` starts a new Pi conversation while retaining the controlled process.

## Roles

- Planner defaults to `read` only.
- Executor defaults to `read,bash,edit,write`.
- Planner and Executor share the `AgentSession` contract; the core owns reset, persistence and turn limits.

## Sandbox

Pi receives a read-only root filesystem, writable `/workspace`, read-only `/task` and `/skills`, no Docker socket, dropped capabilities, `no-new-privileges`, CPU/memory/PID limits. Executors may receive `NET_RAW` explicitly; Planner does not receive shell capability by default.
