# whatweb

Use to identify web servers, frameworks, headers, and common components.

```bash
whatweb -a 1 --color=never URL
whatweb -a 3 --color=never --log-brief=/workspace/whatweb.txt URL
```

Aggression level 1 is usually enough. Treat version matches as hypotheses until
confirmed by source, headers, or behavior.
