---
id: yr-5reb
status: closed
deps: [yr-nnog, yr-aru3]
links: []
created: 2026-02-09T23:07:08Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-5543
---
# E8-T1 Self-host demo: Codex + tk + concurrency=2

STRICT TDD: add acceptance test then run live demo using Codex backend.

## Acceptance Criteria

Given codex backend and tk root, when running concurrency=2, then at least one task lands via merge queue.


## Notes

**2026-02-13T22:33:50Z**

Added acceptance coverage in cmd/yolo-agent/e2e_test.go: TestE2E_CodexTKConcurrency2LandsViaMergeQueue. The test uses tk root + codex adapter stub + concurrency=2 and asserts run_started metadata backend=codex/concurrency=2, at least one merge_landed event, and closed task states. Validation: go test ./cmd/yolo-agent -run TestE2E_CodexTKConcurrency2LandsViaMergeQueue -count=1; go test ./cmd/yolo-agent; go test ./... . Live demo run (throwaway repo with tk + fake codex binary + --agent-backend codex --concurrency 2 --stream) produced merge_landed_count=2 with both tasks closed.
