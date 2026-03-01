---
id: kafka-design
status: in_progress
deps: []
links: []
created: 2026-03-01T11:00:00Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: kafka-epic
---
# Task: Design Kafka Topic Schema and Consumer Strategy

**Epic:** kafka-epic  
**Depends on:** None

## Description

Design the Kafka-specific mapping of Bus interface concepts to Kafka primitives:
- Topic naming conventions (subjects → topics, queues → topics with consumer groups)
- Partition strategy for ordered vs parallel processing
- Consumer group configuration for queue semantics
- Message key strategy for partitioning and ordering

## Acceptance Criteria

- [ ] Document topic naming convention: `{prefix}.{subject}` for pub/sub, `{prefix}.queue.{queue_name}` for queues
- [ ] Document partition strategy: partition by `IdempotencyKey` for ordering within correlation
- [ ] Document consumer group naming: configurable via `BusBackendOptions.Group`
- [ ] Document offset commit strategy: manual commit on Ack, no commit on Nack with re-publish
- [ ] All design decisions documented in task body or linked doc

## TDD Requirement

N/A (design task, no code)

## Related Files

- `internal/distributed/bus.go` - Bus interface reference
- `internal/distributed/redis_bus.go` - Reference implementation
- `internal/distributed/nats_bus.go` - Reference implementation

## Notes

**2026-03-01T16:40:42Z**

yolo.inbox.comment=blocked: no executor matches required capabilities/environment/credentials

**2026-03-01T16:40:44Z**

yolo.inbox.comment=Task: Design Kafka Topic Schema and Consumer Strategy

**2026-03-01T16:49:18Z**

yolo.inbox.comment=Task: Design Kafka Topic Schema and Consumer Strategy

**2026-03-01T16:49:20Z**

decision=retry

**2026-03-01T16:49:20Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:20Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:20Z**

review_retry_count=1

**2026-03-01T16:49:20Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:20Z**

decision=retry

**2026-03-01T16:49:20Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:20Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:20Z**

review_retry_count=2

**2026-03-01T16:49:20Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:20Z**

decision=retry

**2026-03-01T16:49:20Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:20Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:20Z**

review_retry_count=3

**2026-03-01T16:49:20Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:20Z**

decision=retry

**2026-03-01T16:49:20Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:20Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:20Z**

review_retry_count=4

**2026-03-01T16:49:20Z**

triage_reason=review verdict missing explicit pass
