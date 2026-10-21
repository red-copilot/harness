# socat

Use for protocol interaction and scoped TCP forwarding.

```bash
socat - TCP:TARGET:PORT,connect-timeout=5
socat TCP-LISTEN:LOCAL_PORT,bind=127.0.0.1,reuseaddr,fork \
  TCP:INTERNAL_HOST:INTERNAL_PORT,connect-timeout=10
```

The forward example works only when the machine running `socat` can reach the
internal host. Keep listeners bound to loopback, stop them after use, and never
expose an unrestricted forward to shared or public interfaces.
