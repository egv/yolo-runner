---
id: yolo-runner-77c.3.8
status: open
deps: [yolo-runner-77c.3.7]
links: []
created: 2026-01-28T01:04:16.42363+03:00
type: task
priority: 2
parent: yolo-runner-77c.3
---
# v1.4: Implement scrollable log list + statusbar

Implement lipgloss layout with viewport list above teacup statusbar.

## Acceptance Criteria

- Log list uses bubbles/viewport
- Statusbar uses teacup and pinned to bottom
- Resizes correctly
- go test ./... passes


