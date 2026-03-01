---
id: kafka-queue
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
# Task: Implement Kafka Bus - Queue (Enqueue and ConsumeQueue)

**Epic:** kafka-epic  
**Depends on:** kafka-connection

## Description

Implement `Enqueue()` and `ConsumeQueue()` for durable queue semantics using Kafka consumer groups.

## Acceptance Criteria

- [ ] `Enqueue(ctx, queue, event)` sends to topic `{prefix}.queue.{queue_name}`
- [ ] `ConsumeQueue(ctx, queue, opts)` creates consumer group for queue topic
- [ ] Consumer group name from `opts.Group` or `BusBackendOptions.Group`
- [ ] `QueueMessage.Ack(ctx)` commits offset for the message
- [ ] `QueueMessage.Nack(ctx)` does NOT commit offset; message redelivered after rebalance
- [ ] Support for manual re-queue on Nack (re-publish to dead-letter or same topic)
- [ ] Unit tests with mock consumer group pass
- [ ] Contract test `TestMemoryBusQueueNackRedeliversAndAckCompletes` equivalent passes

## TDD Requirement

- Write tests first for: enqueue success, consume receive, ack commits offset, nack no commit
- Test queue recovery after consumer restart (use mock or integration test)

## Related Files

- `internal/distributed/kafka_bus.go` - Add Enqueue/ConsumeQueue methods
- `internal/distributed/kafka_bus_test.go` - Unit tests
