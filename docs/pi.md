# Pi RPC

Pi is launched with:

```text
pi --mode rpc --no-session --no-approve --no-context-files --no-extensions \
   --no-mcp --no-prompt-templates --no-themes --no-skills \
   --skill /skills/tools --skill /skills/tsec-submit \
   --tools read,bash,edit,write --offline
```

The SDK speaks strict LF-delimited JSONL. It never uses a line reader that
treats Unicode line/paragraph separators as protocol boundaries. Each command
has a unique ID and responses are correlated by that ID. stdout is read
continuously; stderr is persisted as a separate log stream.

A successful `prompt` response only means the prompt was accepted, queued or
handled. The SDK waits for `agent_settled`; it does not treat `agent_end` as
completion. Provider failures and aborts after acceptance are observed through
events and process exit.

The optional exploration workflow uses `turn_end` as its action boundary and
advances a fresh-context executor turn. The core itself does not synthesize
frontier state from direct sessions. Operators can still steer a live session
through `Run.Steer`; Pi delivers steering after the current turn's tool calls and
before the next LLM call.

Before launch, the session scans the mounted `/skills` directory and passes
every directory containing `SKILL.md` to Pi with `--skill`. Adding another
embedded or task-provided skill therefore makes it visible in Pi's available
skills list without changing the command construction.

The default command uses a read-only container root, a writable `/workspace`,
read-only `/task` and `/skills`, dropped capabilities plus `NET_RAW`, and no
Docker socket.
