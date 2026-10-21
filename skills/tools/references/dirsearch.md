# dirsearch

Use for recursive web path discovery with built-in extension handling.

```bash
dirsearch -h
dirsearch -u URL -o /workspace/dirsearch.txt
```

Start with defaults. Select extension, recursion, status, thread, and timeout
options from `dirsearch -h` only after fingerprinting or observed URLs justify
them. Review discovered paths with `curl` before launching another wordlist.
