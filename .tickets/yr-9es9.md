---
id: yr-9es9
status: open
deps: [yr-w8yz]
links: []
created: 2026-02-10T00:15:30Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-qxrw
---
# E10-T14 Define and implement webhook-to-worker job contract

STRICT TDD: failing tests first. Define durable job payload/idempotency contract for AgentSession work dispatched from webhook server to worker.

## Acceptance Criteria

Given duplicate webhook deliveries, when jobs are enqueued, then worker deduplicates idempotently and processes exactly once semantics for session step.

