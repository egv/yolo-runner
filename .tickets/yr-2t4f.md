---
id: yr-2t4f
status: closed
deps: []
links: []
created: 2026-02-09T23:42:04Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-0xy1
---
# E1-T10 Define error taxonomy and remediation map

STRICT TDD: enumerate all major failure classes (git/vcs, tracker, runner init, runner timeout/stall, review gating, merge queue conflict, auth/profile/config, filesystem/clone, lock contention, unknown) and map each to user-facing actionable message.

## Acceptance Criteria

Given any known failure class, when error is emitted, then message includes category, cause, and next-step remediation.

