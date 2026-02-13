---
id: yr-xl5o
status: closed
deps: [yr-yg5e]
links: []
created: 2026-02-09T23:07:08Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-lbx1
---
# E5-T5 Linear end-to-end demo

STRICT TDD: add e2e test then demo flow with at least one task closed.

## Acceptance Criteria

Given Linear profile, when yolo-agent runs, then at least one Linear issue is processed end-to-end.


## Notes

**2026-02-13T23:04:26Z**

Added acceptance coverage in cmd/yolo-agent/e2e_test.go: TestE2E_LinearProfileProcessesAndClosesIssue. The test uses a Linear profile from .yolo-runner/config.yaml, builds a real Linear TaskManager against an httptest GraphQL endpoint, runs yolo-agent loop end-to-end, and asserts run_started metadata tracker=linear/profile=linear-demo, workflow status transitions in_progress->closed, and final issue status closed. Validation: go test ./cmd/yolo-agent -run TestE2E_LinearProfileProcessesAndClosesIssue -count=1; go test ./cmd/yolo-agent; go test ./... . Demo flow result: the Linear issue iss-linear-e2e was processed and closed end-to-end.
