---
id: yr-ggdz
status: open
deps: [yr-yhb9]
links: []
created: 2026-02-09T23:07:08Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-lbx1
---
# E5-T2 Map Linear Issue+Project to task graph

STRICT TDD: failing tests first. MVP scope excludes cycles/custom fields.

## Acceptance Criteria

Given Linear issues/projects, when mapped, then parent/dependency/priority model is consistent.


## Notes

**2026-02-13T22:34:08Z**

Implemented strict-TDD Linear issue+project task graph mapping in internal/linear (project root + issue parent fallback, dependency normalization/filtering, Linear priority normalization, deterministic child ordering). Added mapping tests. Validation: go test ./internal/linear && go test ./... . Commit: 3d92627.
