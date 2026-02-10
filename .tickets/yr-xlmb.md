---
id: yr-xlmb
status: closed
deps: [yr-kx2t]
links: []
created: 2026-02-09T23:07:07Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-0xy1
---
# E1-T4 Implement clone manager (full clone per task)

STRICT TDD: failing tests first. Create isolated full repo clone per running task and cleanup policy.

## Acceptance Criteria

Given parallel tasks, when running, then each task has isolated clone path and cleanup succeeds.

