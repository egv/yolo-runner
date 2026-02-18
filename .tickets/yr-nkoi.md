---
id: yr-nkoi
status: closed
deps: [yr-w7qh]
links: []
created: 2026-02-10T00:15:30Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-qxrw
---
# E10-T16 Enforce non-inline execution boundary in Linear runtime

STRICT TDD: failing tests first. Add integration guards/tests ensuring webhook process never executes task runtime directly and only dispatches to worker.

## Acceptance Criteria

Given end-to-end created/prompted flow, when traced, then webhook process performs ACK+enqueue only and worker process handles all execution work.

