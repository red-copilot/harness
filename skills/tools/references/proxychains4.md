# proxychains4

Use to send an existing TCP application through an already-established SOCKS
proxy.

Create `/workspace/proxychains.conf`:

```text
strict_chain
proxy_dns
tcp_read_time_out 10000
tcp_connect_time_out 8000
[ProxyList]
socks5 127.0.0.1 1080
```

Then:

```bash
proxychains4 -f /workspace/proxychains.conf nmap -Pn -sT -sV TARGET
proxychains4 -f /workspace/proxychains.conf curl -sS URL
```

Use one proxy per chain and keep scans low-rate. Stop the SOCKS server when the
tunnel is no longer needed.
