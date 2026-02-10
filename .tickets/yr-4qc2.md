---
id: yr-4qc2
status: open
deps: [yr-o96q]
links: []
created: 2026-02-10T00:12:26Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-qxrw
---
# E10-T5 Activities-only conversation reconstruction

STRICT TDD: failing tests first. Build prompt context from Agent Activities only (including prompt activities), not mutable comments.

## Acceptance Criteria

Given prior session history, when prompted event arrives, then reconstructed context uses only immutable Agent Activities.

