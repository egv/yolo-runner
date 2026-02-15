---
id: yr-m0ja
status: closed
deps: [yr-o2y0]
links: []
created: 2026-02-15T14:35:33Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-jp9i
---
# E11-T2 Persist review feedback onto task data/notes

Store review retry metadata and feedback on the task for visibility and recovery.

## Acceptance Criteria

Given extracted feedback, when review fails, then task metadata/notes include review_retry_count, review_feedback, and triage_reason.

