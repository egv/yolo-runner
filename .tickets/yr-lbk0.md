---
id: yr-lbk0
status: closed
deps: [yr-0yna]
links: []
created: 2026-02-09T23:07:07Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-vh4d
---
# E3-T3 Conflict handler with single auto-retry

STRICT TDD: failing tests first. Retry once via rebase/merge, then block with triage metadata.

## Acceptance Criteria

Given conflict, when first retry fails, then task is blocked with triage_status and triage_reason.

