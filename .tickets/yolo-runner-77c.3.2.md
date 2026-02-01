---
id: yolo-runner-77c.3.2
status: closed
deps: [yolo-runner-77c.3.1]
links: []
created: 2026-01-28T01:03:04.107938+03:00
type: task
priority: 2
parent: yolo-runner-77c.3
---
# v1.4: Implement log bubble store

Implement log bubble store that upserts tool-call bubbles by id for updates.

## Acceptance Criteria

- Store upserts tool-call bubbles by id
- Existing bubble updates on new status
- Minimal public API for list view
- go test ./... passes


