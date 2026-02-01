---
id: yolo-runner-127.4.25
status: open
deps: [yolo-runner-127.4.14]
links: []
created: 2026-01-27T16:46:06.638442+03:00
type: task
priority: 2
parent: yolo-runner-127.4
---
# v1.3: Strip control sequences from agent text

Strip/normalize control sequences like \n/\r from agent_message and agent_thought output before rendering.

## Acceptance Criteria

- Agent message/thought output has no raw control sequences (\n, \r, \t) in TUI/headless
- Normalized text preserves readable spacing
- Tests cover newline/carriage return normalization
- go test ./... passes


