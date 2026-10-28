# First-Principles Harness v2 Implementation Plan

- Status: Implemented

## Delivered Changes

1. Split public domain contracts into `core` and control implementation into `engine`.
2. Reduced the root package to a thin façade.
3. Removed the writable Branch/Knowledge control path.
4. Required exactly two Executors and renamed Planner to Reasoner.
5. Added bounded Context Builder and Reasoner failure cap.
6. Added `CanonicalWriter.Commit` with atomic board revision, normalized records and events.
7. Added append-only Intent/Attempt version tables and SQLite schema v6.
8. Replaced direct blackboard writes with atomic commits for initial Facts, Intent admission/claim, Attempt conclusions and cancellation.
9. Added graph board revision and Reasoner activity projection.
10. Updated CLI examples, docs, import boundaries and tests for `harness/v2`.
11. Preserved legacy SQLite Branch/Knowledge tables and Web legacy rendering as read-only compatibility.
12. Updated the read-only Web contract for `reasoner`, bounded policies, new attempt events and graph metadata.

## Verification

Required:

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/harness
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
```

Docker and live TSec/VPN checks remain environment-dependent.
