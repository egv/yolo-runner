---
id: yr-vhmh
status: closed
deps: [yr-i4ph, yr-ggdz]
links: []
created: 2026-02-09T23:07:08Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-lbx1
---
# E5-T3 Implement Linear read operations

STRICT TDD: failing tests first. Implement next/show for Linear adapter.

## Acceptance Criteria

Given Linear project backlog, when querying, then next/selectable tasks are returned correctly.


## Notes

**2026-02-13T22:45:36Z**

Implemented strict-TDD Linear read operations in internal/linear: added GraphQL-backed NextTasks/GetTask, project->task graph loading, dependency-ready filtering, issue-status normalization, parent leaf fallback, and dependency metadata mapping. Added failing-first tests for selectable task ordering/dependency gating, issue-root leaf fallback, and GetTask mapping. Validation: go test ./internal/linear -count=1 && go test ./... -count=1. Commit: df58c3e.
