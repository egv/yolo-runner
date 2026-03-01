---
id: kafka-contract
status: open
deps:
  - kafka-pubsub
  - kafka-queue
  - kafka-requestreply
links: []
created: 2026-03-01T11:00:00Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: kafka-epic
---
# Task: Add Kafka Bus Contract Tests

**Epic:** kafka-epic  
**Depends on:** kafka-pubsub, kafka-queue, kafka-requestreply

## Description

Create `kafka_bus_test.go` with fake/mock Kafka implementation for unit testing all Bus methods.

## Acceptance Criteria

- [ ] `fakeKafkaClient` implements sarama client interfaces for testing
- [ ] All tests in `bus_contract_test.go` pass when run with `KafkaBus` backed by fakes
- [ ] Edge cases tested: connection failure, topic creation, consumer group rebalance
- [ ] No external Kafka required for unit tests (all mocked)
- [ ] Test coverage ≥ 85% for `kafka_bus.go`

## TDD Requirement

This IS the TDD task - implement fake first, then verify KafkaBus against it

## Related Files

- `internal/distributed/kafka_bus_test.go` - Create this file with fakes
- `internal/distributed/bus_contract_test.go` - Reference contract tests
