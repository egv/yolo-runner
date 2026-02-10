---
id: yr-eoib
status: closed
deps: [yr-kx2t, yr-2t4f]
links: []
created: 2026-02-09T23:39:47Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-0xy1
---
# E1-T9 Improve meaningful runtime error messages

STRICT TDD: add failing tests first, then improve agent/runner error surfaces so failures include actionable cause and next step (e.g., dirty worktree, checkout failure, auth/profile missing, merge conflict state).

## Acceptance Criteria

Given common runtime failures, when yolo-agent exits, then stderr contains specific cause + actionable remediation instead of generic 'exit status 1'.


## Notes

**2026-02-10T01:16:49Z**

validated while implementing yr-xolw: actionable stderr still satisfies E1-T9 expectations
