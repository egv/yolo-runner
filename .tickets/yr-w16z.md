---
id: yr-w16z
status: open
deps: [yr-z803]
links: []
created: 2026-02-10T00:12:26Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-qxrw
---
# E10-T3 Enforce first-thought 10s SLA

STRICT TDD: failing tests first. Emit immediate thought activity within 10 seconds for created sessions; add watchdog and fallback path.

## Acceptance Criteria

Given created event, when processing starts, then first thought activity is emitted within 10s or explicit SLA error is recorded.

