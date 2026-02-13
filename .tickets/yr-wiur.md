---
id: yr-wiur
status: closed
deps: [yr-xl5o, yr-nnog]
links: []
created: 2026-02-09T23:07:08Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-5543
---
# E8-T3 Self-host demo: Kimi + Linear

STRICT TDD: add acceptance test then run Kimi backend on Linear issues.

## Acceptance Criteria

Given kimi+linear profile, when run completes, then at least one issue closes end-to-end.


## Notes

**2026-02-13T23:09:34Z**

Implemented strict TDD acceptance coverage for Kimi+Linear in cmd/yolo-agent/e2e_test.go via TestE2E_KimiLinearProfileProcessesAndClosesIssue. Added a fake Kimi CLI binary helper and exercised yolo-agent end-to-end against a Linear httptest GraphQL endpoint with backend=kimi, asserting run_started metadata (backend/profile/tracker), runner_finished kimi review verdict metadata, in_progress->closed workflow transitions, and final issue status closed. Validation: go test ./cmd/yolo-agent -run TestE2E_KimiLinearProfileProcessesAndClosesIssue -count=1; go test ./cmd/yolo-agent -count=1; go test ./... -count=1.
