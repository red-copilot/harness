# ftp

Use for authorized FTP login, listing, and downloading evidence.

Create `/workspace/ftp.commands`:

```text
passive
binary
ls -la
pwd
bye
```

Then:

```bash
ftp -inv TARGET < /workspace/ftp.commands
```

Prefer `curl` for a single anonymous transfer:

```bash
curl --max-time 20 -sS ftp://TARGET/ -o /workspace/ftp-listing.txt
```

Do not download unrelated bulk data. Record filenames and hashes before using
their contents.
