---
id: yolo-runner-127.1
status: closed
deps: [yolo-runner-127.4]
links: []
created: 2026-01-18T23:31:02.344149+03:00
type: epic
priority: 3
parent: yolo-runner-127
---
# v1.2: Runner supports beads molecules

Deferred: add beads molecule traversal support to Go runner selection.

Selection semantics:
- Container issue types: epic, molecule
- Container traversable statuses: open OR in_progress
- Leaf task selectable statuses: open only

Note:
- We are prioritizing runner observability (logging/TUI/watchdog) first.

## Acceptance Criteria

- Given epic->molecule->task trees, selection traverses molecules like epics
- Containers traversed when status is open or in_progress
- Leaf tasks selected only when status is open
- go test ./... passes


