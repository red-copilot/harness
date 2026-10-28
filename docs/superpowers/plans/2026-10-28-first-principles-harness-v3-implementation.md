# First-Principles Harness v3 Implementation Plan

- Status: Implemented

## Delivered

1. Renamed Fact to Claim and made origin/goal explicit anchors.
2. Replaced the Executor array with one fixed Executor slot.
3. Added strict `report.json` parsing; assistant narrative JSON is ignored.
4. Removed live steering and added bounded, audited Hints.
5. Moved artifact bytes into SQLite schema v7.
6. Removed legacy Branch/Knowledge tables and Web projections.
7. Added append-only triggers and a canonical event hash chain.
8. Added canonical verifier evidence bundles.
9. Added authority to Verdict and removed Run status projection fields.
10. Added OpenAPI-generated Go and TypeScript contracts.
11. Updated CLI configuration to `harness/v3`.

## Verification

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/harness
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
git diff --check
```
