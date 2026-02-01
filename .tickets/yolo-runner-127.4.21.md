---
id: yolo-runner-127.4.21
status: blocked
deps: []
links: []
created: 2026-01-27T12:05:18.54038+03:00
type: task
priority: 2
parent: yolo-runner-127.4
---
# v1.3: Strip newlines from agent thoughts

Strip or normalize newline characters in ACP agent thoughts before rendering so they don't break the TUI layout.

## Acceptance Criteria

- Agent thought output has no raw newlines (\n, \r) in TUI/headless
- Thought text is normalized to single spaces
- Add formatting test
- go test ./... passes


