---
id: kafka-pubsub
status: open
deps:
  - kafka-connection
links: []
created: 2026-03-01T11:00:00Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: kafka-epic
---
# Task: Implement Kafka Bus - Publish and Subscribe

**Epic:** kafka-epic  
**Depends on:** kafka-connection

## Description

Implement `Publish()` and `Subscribe()` methods for pub/sub messaging using Kafka topics.

## Acceptance Criteria

- [ ] `Publish(ctx, subject, event)` sends to Kafka topic `{prefix}.{subject}`
- [ ] Message key set to `event.IdempotencyKey` for partition ordering
- [ ] `Subscribe(ctx, subject)` creates consumer for topic, returns `<-chan EventEnvelope`
- [ ] Subscriber uses consumer group from `BusBackendOptions.Group` (default: "subscribers")
- [ ] Unsubscribe function stops consumer and closes channel
- [ ] Context cancellation properly stops consumption
- [ ] Unit tests with mock producer/consumer pass
- [ ] Contract tests from `bus_contract_test.go` pass (run against KafkaBus)

## TDD Requirement

- Write tests first for: publish success, publish failure, subscribe receive, subscribe close, context cancel
- Use interface-based mocks for sarama.SyncProducer and sarama.ConsumerGroup

## Related Files

- `internal/distributed/kafka_bus.go` - Add Publish/Subscribe methods
- `internal/distributed/kafka_bus_test.go` - Unit tests
