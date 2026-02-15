---
id: yr-zv9d
status: open
deps: [yr-n8wy, yr-94xu]
links: []
created: 2026-02-15T17:56:44Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-bs0w
---
# E12-T4 Implement config validate command behavior

Implement yolo-agent config validate to run full config checks and return deterministic exit status.

## Acceptance Criteria

validate exits 0 on valid config and 1 on invalid config with deterministic output; strict TDD evidence includes initial failing tests and final passing suite command in notes.

