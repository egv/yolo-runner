---
id: yr-4qc2
status: closed
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


## Notes

**2026-02-13T22:39:45Z**

Implemented strict-TDD activities-only conversation reconstruction in internal/linear. Added ReconstructPromptContext to build deterministic prompt context from immutable AgentActivity history (including prompt activities) and intentionally ignore mutable comment fields. Added tests proving comments are excluded while prior/current prompt activities are included. Validation: go test ./internal/linear; go test ./... . Commit: 7b8ea91.
