# gobuster

Use for straightforward directory, file, DNS, or virtual-host enumeration.

```bash
gobuster dir -u URL -w /usr/share/wordlists/dirb/common.txt \
  -t 4 --timeout 10s -o /workspace/gobuster.txt
```

Prefer `ffuf` when response filtering or precise status/size matching is
required. Keep thread counts low when requests pass through a shared proxy.
