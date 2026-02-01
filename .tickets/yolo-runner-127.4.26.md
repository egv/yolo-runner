---
id: yolo-runner-127.4.26
status: open
deps: [yolo-runner-127.4.14, yolo-runner-127.4.25]
links: []
created: 2026-01-27T16:46:06.7149+03:00
type: task
priority: 2
parent: yolo-runner-127.4
---
# v1.3: Merge and render agent thoughts as markdown

Merge adjacent agent_thought chunks into a single thought and render it as markdown in the UI.

## Acceptance Criteria

- Consecutive agent_thought chunks merge into one message
- Rendered using markdown (no raw markdown in output)
- Tests cover merge boundary and markdown render
- go test ./... passes


