# nmap

Use for service, version, port, and lightweight script checks. Prefer it over a
hand-written `/dev/tcp` loop when the agent can route to the target.

```bash
nmap -Pn -sT -sV --version-light --top-ports 100 -oN /workspace/nmap-top.txt TARGET
nmap -Pn -sT -p- --min-rate 1000 -oN /workspace/nmap-ports.txt TARGET
nmap -Pn -sT -sV -sC -p PORT[,PORT] -oN /workspace/nmap-services.txt TARGET
```

Use `-sT` because the Pi container may not have usable raw-socket privileges.
Keep scripts at the default safe level unless broader checks are authorized.
Do not scan public ranges or the VPN provider network.
