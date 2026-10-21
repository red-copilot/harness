# ssh

Use for authorized shell access and local or dynamic forwarding.

```bash
ssh -o ConnectTimeout=8 -o StrictHostKeyChecking=no \
  -o UserKnownHostsFile=/dev/null USER@TARGET
ssh -N -L 127.0.0.1:LOCAL_PORT:INTERNAL_HOST:INTERNAL_PORT USER@TARGET
ssh -N -D 127.0.0.1:SOCKS_PORT USER@TARGET
```

Inspect `/etc/hosts`, routes, listening sockets, and readable evidence after
login. Do not alter accounts, persistence, or remote configuration unless the
authorized objective explicitly requires it.
