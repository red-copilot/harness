# nuclei

Use for template-driven confirmation of known web vulnerabilities and exposed
components.

```bash
nuclei -u URL -severity low,medium,high,critical -rl 20 \
  -timeout 10 -retries 1 -o /workspace/nuclei.txt
```

Start with default templates and the target's observed technology. Do not run
intrusive templates without checking the template name and tags. Validate each
hit manually before relying on it.
