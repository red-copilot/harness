# jq

Use to extract compact evidence from JSON tool output and APIs.

```bash
jq '.results[] | {status,length,url}' /workspace/ffuf.json
jq -r '.[] | [.host,.port,.service] | @tsv' /workspace/nmap.json
```

Select only fields needed for the next decision. Store large responses in the
workspace and query them instead of pasting them into the conversation.
