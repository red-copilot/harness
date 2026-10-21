# netcat

Use for a single TCP banner check, line-oriented protocol test, or local
listener when `socat` is unavailable.

```bash
nc -nv -w 5 TARGET PORT
printf 'QUIT\r\n' | nc -nv -w 5 TARGET PORT
nc -lvnp 127.0.0.1 9000
```

Prefer `curl` for HTTP and `ftp` for FTP. Do not use command-execution flags or
leave listeners running after the test.
