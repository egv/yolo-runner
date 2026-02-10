---
id: yr-dsr0
status: closed
deps: [yr-kx2t]
links: []
created: 2026-02-09T23:07:07Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-0xy1
---
# E1-T3 Add fixed worker pool concurrency flag

STRICT TDD: failing tests first. Add --concurrency N and enforce max active workers.

## Acceptance Criteria

Given N workers, when queue is loaded, then active executions never exceed N.

