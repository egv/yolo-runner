---
id: yolo-runner-127.4.6
status: closed
deps: []
links: []
created: 2026-01-19T15:55:27.595872+03:00
type: task
priority: 3
parent: yolo-runner-127.4
---
# v1.2: Update README for TUI/headless

Document the TUI behavior and the --headless flag.

Files:
- Modify: README.md

Acceptance:
- README explains default TUI on TTY
- README documents --headless
- go test ./... passes

## Acceptance Criteria

- README documents TUI and --headless


