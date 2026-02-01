---
id: yolo-runner-127.4.4
status: closed
deps: []
links: []
created: 2026-01-19T15:55:27.438163+03:00
type: task
priority: 1
parent: yolo-runner-127.4
---
# v1.2: Headless mode flag

Add CLI flag --headless to force plain output (no Bubble Tea), even when stdout is a TTY.

Files:
- Modify: cmd/yolo-runner/main.go
- Modify: cmd/yolo-runner/main_test.go

Acceptance:
- Given stdout is a TTY, default is TUI
- Given --headless, runner does not start Bubble Tea
- go test ./... passes

## Acceptance Criteria

- Default TUI on TTY
- --headless disables TUI
- go test ./... passes


