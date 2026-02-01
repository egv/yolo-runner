---
id: yolo-runner-127.4.22
status: blocked
deps: []
links: []
created: 2026-01-27T12:08:13.473382+03:00
type: task
priority: 2
parent: yolo-runner-127.4
---
# v1.3: Simplify tool call update line

Format tool_call_update output as '🔄 tool_call_update: <title>' (no id/kind/status fields) now that emoji/colors indicate status.

## Acceptance Criteria

- tool_call_update lines show only emoji + label + title
- call id/kind/status not printed
- formatting test added
- go test ./... passes


