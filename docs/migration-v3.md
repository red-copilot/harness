# Migrating to Red Harness v3

v3 is a clean break. It does not open, upgrade or import v2 databases in place.

1. Stop v2 writers.
2. Archive the v2 SQLite database and its external artifact directory.
3. Verify the archive hashes and row counts with the v2 release.
4. Keep the archive as the rollback boundary.
5. Create a fresh v3 database with `sqlite.OpenFresh`.
6. Use `apiVersion: harness/v3`, one Reasoner and one Executor.
7. Run `harness doctor`; it verifies schema v7 and the canonical event hash chain.

v3 stores evidence bytes in SQLite, so no artifact directory is required for new Runs. Rollback requires restoring the v2 archive and running the matching v2 binary.
