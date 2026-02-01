---
id: yolo-runner-77c.3
status: open
deps: [yolo-runner-77c.2]
links: []
created: 2026-01-27T16:31:16.442708+03:00
type: epic
priority: 2
parent: yolo-runner-77c
---
# v1.4: Bubbletea TUI redesign

Refactor the TUI into a bubble/viewport log list with per-event bubbles, pinned teacup statusbar, and markdown-rendered agent thoughts. Tracks the full UI redesign described in UI_REDESIGN.md.

## Acceptance Criteria

- TUI uses bubble list of log entries (tool calls, agent messages, status)
- Statusbar uses teacup and stays pinned to bottom
- Agent thoughts render as markdown via teacup
- Log list scrolls and resizes correctly
- Tool call bubbles update in place by id
- go test ./... passes


