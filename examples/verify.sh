#!/bin/sh
# Example verifier: only the presence of the agent-produced proof file is
# accepted, and the verdict is emitted as JSON on stdout.
input=$(cat)
root=$(printf '%s' "$input" | sed -n 's/.*"workspace_root":"\([^"]*\)".*/\1/p')
if [ -n "$root" ] && [ -f "$root/sessions/pi/proof.txt" ]; then
  printf '%s\n' '{"success":true,"score":100,"message":"proof.txt exists","evidence":[{"kind":"file","path":"sessions/pi/proof.txt"}]}'
else
  printf '%s\n' '{"success":false,"score":0,"message":"proof.txt is missing"}'
fi
