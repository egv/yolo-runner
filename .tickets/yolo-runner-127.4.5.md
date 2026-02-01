---
id: yolo-runner-127.4.5
status: closed
deps: []
links: []
created: 2026-01-19T15:55:27.519065+03:00
type: task
priority: 2
parent: yolo-runner-127.4
---
# v1.2: Detect OpenCode log growth for heartbeat

Implement a watcher that polls the per-task OpenCode JSONL log file size and sends an event when it grows.

Files:
- Create: internal/ui/tui/logwatch.go
- Create: internal/ui/tui/logwatch_test.go

Acceptance:
- Given a file grows, watcher emits an "output" event
- Given no growth, watcher emits nothing
- Tests use temp files and a fake clock/ticker
- go test ./... passes

## Acceptance Criteria

- Emits event on file growth
- No event when no growth
- Deterministic tests
- go test ./... passes


