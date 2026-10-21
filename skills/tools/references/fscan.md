# fscan

Use fscan for bounded host and service discovery when the current agent can
route directly to an authorized subnet. It combines alive checks, common-port
scanning, service fingerprints, and optional credential or Web checks.

Inspect the pinned release before composing a scan:

```bash
fscan -help
```

`-h` is the target flag in fscan 1.8.4, not help. Do not run bare `fscan -h`.
The installed release is authoritative.

Start with an explicit target, bounded concurrency, saved output, and optional
active checks disabled:

```bash
TARGET=192.0.2.10

fscan -h "$TARGET" -nobr -nopoc -t 50 -time 3 \
  -json -o /workspace/fscan.json
```

Use `-hf HOSTS_FILE` only when every address in the file is authorized. Add
ports or service modules from `fscan -help` only after direct reachability or
earlier evidence justifies them.

## Scope and proxy limits

- Do not scan broad ranges merely to enumerate an unknown environment.
- Lower `-t` and increase `-time` when using a shared network or unstable VPN.
- Keep `-nobr` and `-nopoc` for initial discovery.
- Enable `-m ssh`, `-m ftp`, or password options only after identifying the
  service and assembling a small evidence-based candidate list.
- `-proxy` is for fscan's Web/POC path, not a transparent TCP proxy.
- `-socks5` requires a real SOCKS5 listener. A request proxy such as
  `proxy.php?url=...` cannot carry fscan traffic.
- If an HTTP service starts returning `429`, `502`, or connection errors, stop
  scanning and return to the last proven foothold.

For a flat authorized internal network, begin with fscan to identify live
services, then move to the tool-specific reference for validation:

- HTTP: `whatweb`, `ffuf`, `nuclei`, `curl`
- SSH/FTP: `ssh`, `sshpass`, `hydra`, `ftp`
- Tunneling: `chisel`, `socat`, `proxychains4`
