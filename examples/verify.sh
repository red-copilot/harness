#!/bin/sh
# Example verifier: accepts explicit proof.txt evidence under the Run workspace.
input=$(cat)
root=$(printf '%s' "$input" | sed -n 's/.*"workspace_root":"\([^"]*\)".*/\1/p')
if [ -n "$root" ] && find "$root" -name proof.txt -type f | grep -q .; then
  printf '%s\n' '{"success":true,"score":100,"message":"proof.txt exists","evidence":[{"kind":"file","path":"proof.txt"}]}'
else
  printf '%s\n' '{"success":false,"score":0,"message":"proof.txt is missing"}'
fi
