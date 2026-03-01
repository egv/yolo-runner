---
id: kafka-integration
status: open
deps:
  - kafka-contract
links: []
created: 2026-03-01T11:00:00Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: kafka-epic
---
# Task: Add Kafka Bus Integration Tests

**Epic:** kafka-epic  
**Depends on:** kafka-contract

## Description

Create `kafka_bus_integration_test.go` with tests against real Kafka broker (via Podman).

## Acceptance Criteria

- [ ] Tests skip if `KAFKA_TEST_ADDR` env var not set
- [ ] Test: full pub/sub cycle with real Kafka
- [ ] Test: queue consume with ack/nack and offset commit verification
- [ ] Test: consumer group rebalance with multiple consumers
- [ ] Test: request-reply with timeout
- [ ] All integration tests pass against local Kafka via Podman Compose

## TDD Requirement

Write test scenarios first, then ensure they pass with real Kafka

## Related Files

- `internal/distributed/kafka_bus_integration_test.go` - Create this file
- `compose.kafka.yaml` - Podman Compose for local Kafka
