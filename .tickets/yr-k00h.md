---
id: yr-k00h
status: closed
deps: [yr-m0ja]
links: []
created: 2026-02-15T14:35:33Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-jp9i
---
# E11-T3 Add review-fail retry policy and counters

Implement configurable retry budget N for review-fail remediation path only.

## Acceptance Criteria

Given review fail and retries remaining, when loop advances, then implement+review is retried; non-review failures do not use this retry path.

