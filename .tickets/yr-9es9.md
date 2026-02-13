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


## Notes

**2026-02-13T22:53:59Z**

Implemented webhook-to-worker job contract v1 for Linear AgentSession dispatch: added explicit Job contract fields (contractVersion, sessionId, sessionStep, idempotencyKey), deterministic session-step key derivation for created/prompted events, and idempotency namespace key generation for worker dedupe. Updated handler job builder to populate contract fields and use idempotency key as fallback job ID. Added strict TDD coverage in internal/linear/webhook/contract_test.go and jsonl_queue_test.go plus handler assertions to verify durable serialization and duplicate-delivery stability. Validation: go test ./internal/linear/webhook && go test ./...
