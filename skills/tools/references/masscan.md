# masscan

Use only for explicit, authorized ranges and when `nmap -p-` is too slow.

```bash
masscan TARGET -p1-65535 --rate 500 -oL /workspace/masscan.txt
```

Start at a low rate and scan only the task's containers or confirmed internal
addresses. Follow with `nmap -sV` on open ports. Never use internet-wide ranges
or high rates against shared infrastructure.
