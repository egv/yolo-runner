---
id: yr-yhb9
status: closed
deps: [yr-8nec]
links: []
created: 2026-02-09T23:07:07Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-bbtg
---
# E4-T3 Add tracker conformance suite

STRICT TDD: shared suite for tk and future tracker adapters.

## Acceptance Criteria

Given tk adapter, when suite runs, then all required tracker operations pass contract tests.


## Notes

**2026-02-13T21:57:15Z**

Implemented shared TaskManager conformance suite and tk adapter conformance coverage in strict TDD style. Added internal/contracts/conformance/task_manager_suite.go and internal/tk/conformance_test.go with scenario-based contract checks for next/get/status/data plus dependency behavior. Validation: go test ./internal/tk ./internal/contracts/conformance && go test ./... -timeout 120s. Commit: 6f05d7a.
