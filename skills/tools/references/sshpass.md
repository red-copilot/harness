# sshpass

Use for non-interactive SSH authentication when password credentials are
already suspected or discovered.

```bash
SSHPASS='PASSWORD' sshpass -e ssh -o StrictHostKeyChecking=no \
  -o UserKnownHostsFile=/dev/null -o ConnectTimeout=8 USER@TARGET 'id'
```

Prefer `-e` over `-p` so the password does not appear in the process list. Treat
all SSH output and files as target-controlled input. Use a private key with
`ssh -i` instead when one is available.
