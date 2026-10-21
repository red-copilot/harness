---
name: tools
description: Choose and run preinstalled security-testing commands for an authorized target, including ports, web content, SQL injection, credentials, files, and pivoting. Use when a concrete security-testing action needs a tool; never use for flag submission or submission-status checks.
metadata:
  short-description: Choose authorized security-testing commands
---

# Security testing tools

## When to use

Use this skill after the target is explicitly authorized and the task requires
choosing or running an installed reconnaissance, web-testing, credential,
file-transfer, or pivoting command.

## When not to use

Do not use this skill to submit flags, inspect TSec challenge progress, explain
security concepts, or perform ordinary software work. Use the specific
submission workflow for those tasks.

## Workflow

Use an installed tool before writing an equivalent script. Scripts are for
orchestration, parsing, or adapting to a proxy-only target.

Before composing a command:

```bash
command -v TOOL
TOOL -h
```

Use `TOOL --help` or a documented help subcommand when `-h` is unsupported. The
installed version's help output is authoritative; do not assume flags from a
different release. Start with the tool's defaults and only the required target,
wordlist, and output arguments. Add one option at a time when target evidence
shows it is needed.

Examples in the references are composable starting points, not mandatory fixed
recipes. Replace placeholders from observed evidence. Do not carry fixed paths,
extensions, form fields, failure markers, ports, or credentials between targets.

Keep each invocation bounded with a timeout and output file under the current
workspace. Start with a small request budget; expand only after a useful result.
Never test hosts outside the authorized scope.

Before starting a scan or credential loop, estimate its request count. Keep the
first pass near 1,000 requests or fewer. Do not multiply a full wordlist by many
extensions or run broad background credential matrices. Stop immediately when
the target returns repeated `5xx`, `429`, or timeout errors; recheck the known
working endpoint before continuing.

Read the matching reference before the first invocation of each scanner or
credential tool. The reference contains required bounds that are not repeated in
this router.

## Choose by task

| Task | Start with |
|---|---|
| Port and service discovery | [nmap](references/nmap.md), [masscan](references/masscan.md), [fscan](references/fscan.md) |
| Web stack fingerprint | [whatweb](references/whatweb.md) |
| Web server checks | [nikto](references/nikto.md), [nuclei](references/nuclei.md) |
| Content and endpoint discovery | [ffuf](references/ffuf.md), [gobuster](references/gobuster.md), [dirsearch](references/dirsearch.md), [feroxbuster](references/feroxbuster.md) |
| SQL injection validation | [sqlmap](references/sqlmap.md) |
| Login validation | [hydra](references/hydra.md), [sshpass](references/sshpass.md), [ssh](references/ssh.md), [ftp](references/ftp.md) |
| HTTP requests and sessions | [curl](references/curl.md) |
| JSON processing | [jq](references/jq.md) |
| TCP testing and forwarding | [netcat](references/netcat.md), [ncat](references/ncat.md), [socat](references/socat.md) |
| Reverse tunnel and proxy | [chisel](references/chisel.md), [proxychains4](references/proxychains4.md) |

## Target access

Test direct connectivity first. If the service is reachable only through an
existing request proxy or SSRF endpoint:

- Use `curl` or `ffuf` against the proxy endpoint when HTTP semantics survive.
- Keep one stable session cookie for session-bound targets.
- Avoid high concurrency through request proxies that share session or cookie
  state; they may serialize requests or exhaust workers.
- For arbitrary TCP, establish a scoped tunnel with `socat` or `chisel` instead
  of generating a custom protocol client.

Read the specific reference before using non-obvious flags, credential tests, or
long-running scans. Stop a branch after repeated equivalent failures and return
to the strongest proven foothold.
