---
id: kafka-requestreply
status: open
deps:
  - kafka-pubsub
links: []
created: 2026-03-01T11:00:00Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: kafka-epic
---
# Task: Implement Kafka Bus - Request/Reply Pattern

**Epic:** kafka-epic  
**Depends on:** kafka-pubsub

## Description

Implement `Request()` and `Respond()` for synchronous request-reply pattern.

## Acceptance Criteria

- [ ] `Request(ctx, subject, request, timeout)` publishes request, waits for reply on `{subject}.reply.{idempotency_key}`
- [ ] Reply subject set in `request.ReplyTo`
- [ ] Correlation matching: only accept response with matching `CorrelationID`
- [ ] Timeout returns `context.DeadlineExceeded` error
- [ ] `Respond(ctx, subject, handler)` subscribes to subject, calls handler, publishes reply
- [ ] Unit tests for request success, timeout, correlation mismatch pass
- [ ] Contract test `TestMemoryBusRequestReply` equivalent passes

## TDD Requirement

- Write tests first for: successful request-reply, timeout handling, correlation ID matching

## Related Files

- `internal/distributed/kafka_bus.go` - Add Request/Respond methods
- `internal/distributed/kafka_bus_test.go` - Unit tests
