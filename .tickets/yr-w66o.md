---
id: yr-w66o
status: closed
deps: [yr-5reb]
links: []
created: 2026-02-09T23:07:08Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-5543
---
# E8-T2 Self-host demo: Claude + conflict retry path

STRICT TDD: add acceptance test for one-retry conflict behavior with Claude backend.

## Acceptance Criteria

Given intentional conflict, when queue retries once, then final state is landed or blocked with triage metadata.


## Notes

**2026-02-13T22:40:11Z**

Added acceptance e2e coverage in cmd/yolo-agent/e2e_test.go via TestE2E_ClaudeConflictRetryPathFinalizesWithLandingOrBlockedTriage. The test runs Claude backend with intentional merge conflicts and asserts one merge_retry then final landed or blocked outcome, with blocked path requiring triage metadata (triage_status/triage_reason) on stream events. Also updated fakeVCS in e2e tests to script merge errors and count merge attempts, and added fake Claude binary helper. Validation: go test ./cmd/yolo-agent -run TestE2E_ClaudeConflictRetryPathFinalizesWithLandingOrBlockedTriage -count=1; go test ./cmd/yolo-agent -count=1; go test ./... -count=1.
