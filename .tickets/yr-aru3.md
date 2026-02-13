---
id: yr-aru3
status: closed
deps: [yr-hxps]
links: []
created: 2026-02-09T23:07:07Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-vh4d
---
# E3-T5 Merge queue integration tests

STRICT TDD: add parallel-branch contention tests before polish.

## Acceptance Criteria

Given competing branches, when queue runs, then landing order is serialized and consistent.


## Notes

**2026-02-13T21:13:05Z**

Added parallel branch contention integration coverage in internal/agent/e2e_branch_review_merge_push_test.go; asserts competing branches queue together, landing stays serialized (max in-flight merge=1), and merge/push order remains consistent. Verified with go test ./internal/agent and go test ./...
