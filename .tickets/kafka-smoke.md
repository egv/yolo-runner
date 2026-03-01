---
id: kafka-smoke
status: open
deps:
  - kafka-integration
  - kafka-config
links: []
created: 2026-03-01T11:00:00Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: kafka-epic
---
# Task: Add Kafka Smoke Test and E2E Test

**Epic:** kafka-epic  
**Depends on:** kafka-integration, kafka-config

## Description

Add `kafka_smoke_test.go` and update `distributed_e2e_smoke_test.go` to include Kafka.

## Acceptance Criteria

- [ ] `kafka_smoke_test.go` tests basic pub/sub and queue operations
- [ ] Test skipped if `KAFKA_TEST_ADDR` not set
- [ ] `distributed_e2e_smoke_test.go` includes Kafka in backend matrix
- [ ] All smoke tests pass with real Kafka
- [ ] CI workflow updated to run Kafka tests (optional: depends on CI setup)

## TDD Requirement

Write smoke test scenarios first, verify against real Kafka

## Related Files

- `internal/distributed/kafka_smoke_test.go` - Create this file
- `internal/distributed/distributed_e2e_smoke_test.go` - Update to include Kafka
