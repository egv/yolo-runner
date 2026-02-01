---
id: yolo-runner-127.4.3
status: closed
deps: []
links: []
created: 2026-01-19T15:55:27.357111+03:00
type: task
priority: 1
parent: yolo-runner-127.4
---
# v1.2: Wire runner phases into TUI

Plumb runner phase/state updates into the TUI.

Files:
- Modify: internal/runner/runner.go
- Create: internal/runner/events.go

Acceptance:
- Runner emits structured events for: selecting task, bd update, opencode start/end, git add/status/commit, bd close/verify, bd sync
- TUI displays current phase based on latest event
- go test ./... passes

## Acceptance Criteria

- Runner emits phase events
- TUI updates from events
- go test ./... passes


