# feroxbuster

Use for fast recursive content discovery when its request pattern fits the
target.

```bash
feroxbuster -u URL -w /usr/share/wordlists/dirb/common.txt \
  -t 10 --timeout 10 --depth 1 -o /workspace/feroxbuster.txt
```

Set `-C 404` to filter common noise. Reduce threads drastically for SSRF or
session-bound proxies; prefer `ffuf` when custom filters are needed.
