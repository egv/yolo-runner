---
id: yr-w16z
status: closed
deps: [yr-z803]
links: [yr-p7hw, yr-9es9]
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

**2026-02-13T22:57:37Z**

triage_reason=review verdict returned fail

**2026-02-13T22:57:37Z**

triage_status=failed

**2026-02-18T20:18:31Z**

review_fail_feedback=`internal/linear/first_thought_sla.go` is currently a standalone helper with no production call site, so created-event processing still does not enforce the 10s first-thought SLA end-to-end; wire `EnforceFirstThoughtSLA` into the real AgentSession created workflow (watchdog start + first-thought observed signal), ensure timeout/fallback failures always persist an explicit SLA error, and add integration coverage proving created events either emit thought activity within 10s or record the SLA error.

**2026-02-18T20:18:31Z**

review_feedback=`internal/linear/first_thought_sla.go` is currently a standalone helper with no production call site, so created-event processing still does not enforce the 10s first-thought SLA end-to-end; wire `EnforceFirstThoughtSLA` into the real AgentSession created workflow (watchdog start + first-thought observed signal), ensure timeout/fallback failures always persist an explicit SLA error, and add integration coverage proving created events either emit thought activity within 10s or record the SLA error.

**2026-02-18T20:18:31Z**

review_retry_count=1

**2026-02-18T20:18:31Z**

review_verdict=fail
