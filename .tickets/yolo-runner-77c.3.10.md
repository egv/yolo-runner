---
id: yolo-runner-77c.3.10
status: open
deps: [yolo-runner-77c.3.9, yolo-runner-77c.3.2, yolo-runner-77c.3.4, yolo-runner-77c.3.6]
links: []
created: 2026-01-28T01:04:42.035188+03:00
type: task
priority: 2
parent: yolo-runner-77c.3
---
# v1.4: Implement event routing to log bubbles

Wire ACP/runner events into log bubble store; update existing bubbles by id.

## Acceptance Criteria

- Event router updates bubble store
- Tool call updates refresh existing bubble
- Log list updates reflect new bubbles
- go test ./... passes


