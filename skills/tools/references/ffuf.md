# ffuf

Use for HTTP parameter, path, virtual-host, or value fuzzing. `ffuf` has no
separate file and directory modes: each word from the wordlist replaces `FUZZ`
exactly. A wordlist that contains both names and complete filenames covers both.

Inspect the installed version before selecting optional flags:

```bash
ffuf -h
```

Start with FFUF's defaults and only the required inputs:

```bash
BASE='https://example.test'
WORDLIST='/usr/share/wordlists/dirb/common.txt'

ffuf -u "${BASE%/}/FUZZ" -w "$WORDLIST" \
  -of json -o /workspace/ffuf.json
```

The default matcher, concurrency, rate, and timeout are reasonable first-pass
settings. Use `ffuf -h` to discover the available matcher, filter, request,
proxy, recursion, and output options only when the first response set requires
them.

- Keep the first pass near 1,000 requests or fewer and concurrency at or below
  20. Do not cross-product a full wordlist with a broad extension list.
- Do not use `URL/FUZZ/` for general surface discovery; it excludes root files.
- Do not add target-specific paths or extensions to the reference.
- When `FUZZ` is followed by a literal extension, do not add that same extension
  with `-e`.
- If the wordlist contains stems without extensions and fingerprinting shows
  extension variants are relevant, use the extension option reported by
  `ffuf -h`.
- When the target has a catch-all response, inspect `ffuf -h` and filter stable
  noise with the appropriate size, status, or word matcher.

Use fewer workers or a lower rate through a shared request proxy. For POST data,
build the request flags from the help output and evidence from an equivalent
`curl` request:

```bash
ffuf -s -u "$URL" -w "$WORDLIST" -X POST \
  -H 'Content-Type: application/json' -d '{"key":"FUZZ"}' \
  -of json -o /workspace/ffuf-post.json
```

Save results and inspect them with `jq` before expanding the wordlist or
changing match rules.
