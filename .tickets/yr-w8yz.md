---
id: yr-w8yz
status: open
deps: [yr-o96q, yr-u5k1]
links: []
created: 2026-02-10T00:15:30Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-qxrw
---
# E10-T13 Create standalone yolo-linear-webhook binary

STRICT TDD: failing tests first. Introduce dedicated webhook server binary that only validates, ACKs quickly, and enqueues AgentSession work; it must not run heavy execution inline.

## Acceptance Criteria

Given created/prompted webhook events, when received by yolo-linear-webhook, then response returns within SLA and execution is delegated via queue/job handoff only.

