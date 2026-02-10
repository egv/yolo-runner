---
id: yr-c5hx
status: closed
deps: [yr-dsr0, yr-hxps]
links: []
created: 2026-02-09T23:07:08Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-2y0b
---
# E7-T1 Extend event schema for parallel workers

STRICT TDD: failing tests first. Add worker_id clone_path queue_pos fields.

## Acceptance Criteria

Given parallel execution, when events are emitted, then worker and queue context is present.

