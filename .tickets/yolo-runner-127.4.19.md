---
id: yolo-runner-127.4.19
status: closed
deps: [yolo-runner-127.4.16]
links: []
created: 2026-01-26T18:42:26.624583+03:00
type: task
priority: 2
parent: yolo-runner-127.4
---
# v1.3: Statusbar shows progress [x/y]

Display progress counter [x/y] in the status bar alongside spinner/state. Use total runnable leaf count and completed count.

## Acceptance Criteria

- Status bar displays [x/y] progress
- x increments on task completion/blocked
- y is total runnable leaves under root
- Updates when task changes
- go test ./... passes


