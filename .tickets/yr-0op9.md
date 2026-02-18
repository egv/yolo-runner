---
id: yr-0op9
status: closed
deps: [yr-p7hw]
links: []
created: 2026-02-15T14:35:33Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-jp9i
---
# E11-T5 Terminal failure after review-retry exhaustion

Fail task with final unresolved blocker summary once retry budget is exhausted.

## Acceptance Criteria

Given repeated review fails and no retries left, when loop ends task, then task is marked failed with final triage summary and not merged/closed.

