---
id: yr-o2y0
status: closed
deps: []
links: []
created: 2026-02-15T14:35:33Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-jp9i
---
# E11-T1 Extract structured review-fail feedback

Parse review artifacts/logs into actionable blocker summary plus full feedback text.

## Acceptance Criteria

Given REVIEW_VERDICT: fail, when loop processes review output, then blocker summary and detailed feedback are extracted.


## Notes

**2026-02-15T14:47:27Z**

landing_status=blocked

**2026-02-15T14:47:27Z**

triage_reason=git pull --ff-only origin main failed: error: cannot pull with rebase: You have unstaged changes.
error: Please commit or stash them.: exit status 128

**2026-02-15T14:47:27Z**

triage_status=blocked
