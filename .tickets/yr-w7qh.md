---
id: yr-w7qh
status: open
deps: [yr-9es9, yr-bho4, yr-4qc2]
links: []
created: 2026-02-10T00:15:30Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-qxrw
---
# E10-T15 Implement dedicated Linear session worker entrypoint

STRICT TDD: failing tests first. Add worker binary/entrypoint that consumes queued session jobs and invokes runner/agent execution flow outside webhook request lifecycle.

## Acceptance Criteria

Given queued session jobs, when worker runs, then task execution and activity emission occur asynchronously without blocking webhook server.

