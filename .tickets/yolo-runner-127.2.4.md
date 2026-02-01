---
id: yolo-runner-127.2.4
status: closed
deps: []
links: []
created: 2026-01-19T11:20:28.431154+03:00
type: task
priority: 2
parent: yolo-runner-127.2
---
# v1.2: Add tests for console output

Add unit tests that assert key output lines are printed.

Files:
- Modify: internal/runner/runner_test.go
- Modify: cmd/yolo-runner/main_test.go

Rules:
- Go only

Acceptance:
- Tests cover: start line, end line, and at least one phase line
- Tests do not depend on timing; heartbeat should be testable via injected ticker/clock or a stubbed progress reporter
- go test ./... passes

## Acceptance Criteria

- Tests assert lifecycle output
- Heartbeat testing is deterministic
- go test ./... passes


