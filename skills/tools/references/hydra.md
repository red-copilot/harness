# hydra

Use only after identifying a real authentication service and a small,
evidence-based credential list.

```bash
hydra -h
hydra -L USER_LIST -P PASSWORD_LIST SERVICE://TARGET
hydra -l USER -P PASSWORD_LIST TARGET http-post-form FORM_SPEC
```

For HTTP forms, derive `FORM_SPEC` from the observed request: login path, field
names, and a success or failure condition. Do not reuse another target's fields
or error text. Use `hydra -h` to choose stop, thread, protocol, and output
options. Keep concurrency low and stop after the evidence-based candidates are
exhausted.
