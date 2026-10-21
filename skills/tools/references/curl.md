# curl

Use for direct HTTP requests, cookie handling, redirects, uploads, and validating
proxy behavior.

```bash
curl -sS -D /workspace/headers.txt -o /workspace/body.html URL
curl -sS -c /workspace/cookies.txt -b /workspace/cookies.txt URL
curl -sS -L -X POST -d 'key=value' URL
curl -sS --get --data-urlencode 'url=http://INTERNAL/' PROXY_URL
```

Always set `--max-time`. Preserve status, headers, cookies, and response body
when a flow matters. Avoid printing authorization tokens or other secrets.
