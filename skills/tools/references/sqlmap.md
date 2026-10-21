# sqlmap

Use after a parameter is mechanically confirmed to reach a SQL query.

```bash
sqlmap -u 'URL?id=1' --batch --level=2 --risk=1 --threads=1 \
  --output-dir=/workspace/sqlmap
```

For authenticated requests, save and reuse the exact cookie:

```bash
sqlmap -u 'URL' --cookie='COOKIE' --data='name=value' --batch --level=3 --risk=1
```

Use one stable session and low concurrency. Prefer data extraction over
OS-level features; use `--os-shell`, file reads, or file writes only when the
authorized objective requires them. SQLmap cannot always traverse a nested
SSRF request proxy; in that case use requests captured by `curl` or a small
orchestration script.
