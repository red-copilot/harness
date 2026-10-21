# nikto

Use for a bounded web-server misconfiguration and known-file scan.

```bash
nikto -host URL -nointeractive -maxtime 120s -output /workspace/nikto.txt
```

Use `-Tuning` to narrow checks when the scan is noisy or slow. Do not treat
every reported item as exploitable; verify only findings that advance the
authorized objective.
