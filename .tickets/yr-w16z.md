---
id: yr-w16z
status: closed
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


## Notes

**2026-02-13T22:54:22Z**

Implemented first-thought SLA watchdog in internal/linear with 10s default deadline, fallback thought emission, explicit SLA error recording on fallback failure, and deterministic fallback idempotency key. Added strict TDD coverage in first_thought_sla_test.go and verified with go test ./internal/linear/... and go test ./...
