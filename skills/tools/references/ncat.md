# ncat

Use when Ncat-specific connection, listening, or broker options are useful.

```bash
ncat -v -w 5 TARGET PORT
printf 'QUIT\r\n' | ncat -v -w 5 TARGET PORT
ncat -l 127.0.0.1 9000
```

Prefer plain `nc` for simple banner or line checks and `socat` for persistent
forwarding. Bind listeners to loopback, stop them after use, and do not use
execute options unless the authorized objective explicitly requires them.
