---
id: yr-sxpf
status: closed
deps: [yr-fx2p]
links: []
created: 2026-02-10T00:12:26Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-qxrw
---
# E10-T6 Auto-transition delegated issue to started state

STRICT TDD: failing tests first. On run begin, move issue to first started workflow state when not already started/completed/canceled.

## Acceptance Criteria

Given delegated issue in non-started state, when run begins, then issue is transitioned to lowest-position started state.

