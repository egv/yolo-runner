---
id: kafka-design
status: open
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

**2026-03-01T16:49:20Z**

decision=retry

**2026-03-01T16:49:20Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:20Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:20Z**

review_retry_count=5

**2026-03-01T16:49:20Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:20Z**

decision=failed

**2026-03-01T16:49:20Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:21Z**

review_retry_count=5

**2026-03-01T16:49:21Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:21Z**

triage_status=failed

**2026-03-01T16:49:23Z**

decision=retry

**2026-03-01T16:49:23Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:23Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:23Z**

review_retry_count=1

**2026-03-01T16:49:23Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:24Z**

decision=retry

**2026-03-01T16:49:24Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:24Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:24Z**

review_retry_count=2

**2026-03-01T16:49:24Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:24Z**

decision=retry

**2026-03-01T16:49:24Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:24Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:24Z**

review_retry_count=3

**2026-03-01T16:49:24Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:24Z**

decision=retry

**2026-03-01T16:49:24Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:24Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:24Z**

review_retry_count=4

**2026-03-01T16:49:24Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:24Z**

decision=retry

**2026-03-01T16:49:24Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:24Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:24Z**

review_retry_count=5

**2026-03-01T16:49:24Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:24Z**

decision=failed

**2026-03-01T16:49:24Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:24Z**

review_retry_count=5

**2026-03-01T16:49:24Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:24Z**

triage_status=failed

**2026-03-01T16:49:27Z**

decision=retry

**2026-03-01T16:49:27Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:27Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:27Z**

review_retry_count=1

**2026-03-01T16:49:27Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:27Z**

decision=retry

**2026-03-01T16:49:27Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:27Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:27Z**

review_retry_count=2

**2026-03-01T16:49:27Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:28Z**

decision=retry

**2026-03-01T16:49:28Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:28Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:28Z**

review_retry_count=3

**2026-03-01T16:49:28Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:28Z**

decision=retry

**2026-03-01T16:49:28Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:28Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:28Z**

review_retry_count=4

**2026-03-01T16:49:28Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:28Z**

decision=retry

**2026-03-01T16:49:28Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:28Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:28Z**

review_retry_count=5

**2026-03-01T16:49:28Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:28Z**

decision=failed

**2026-03-01T16:49:28Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:28Z**

review_retry_count=5

**2026-03-01T16:49:28Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:28Z**

triage_status=failed

**2026-03-01T16:49:31Z**

decision=retry

**2026-03-01T16:49:31Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:31Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:31Z**

review_retry_count=1

**2026-03-01T16:49:31Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:31Z**

decision=retry

**2026-03-01T16:49:31Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:31Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:31Z**

review_retry_count=2

**2026-03-01T16:49:31Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:31Z**

decision=retry

**2026-03-01T16:49:31Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:31Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:31Z**

review_retry_count=3

**2026-03-01T16:49:31Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:32Z**

decision=retry

**2026-03-01T16:49:32Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:32Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:32Z**

review_retry_count=4

**2026-03-01T16:49:32Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:32Z**

decision=retry

**2026-03-01T16:49:32Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:32Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:32Z**

review_retry_count=5

**2026-03-01T16:49:32Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:32Z**

decision=failed

**2026-03-01T16:49:32Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:32Z**

review_retry_count=5

**2026-03-01T16:49:32Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:32Z**

triage_status=failed

**2026-03-01T16:49:35Z**

decision=retry

**2026-03-01T16:49:35Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:35Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:35Z**

review_retry_count=1

**2026-03-01T16:49:35Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:35Z**

decision=retry

**2026-03-01T16:49:35Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:35Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:35Z**

review_retry_count=2

**2026-03-01T16:49:35Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:35Z**

decision=retry

**2026-03-01T16:49:35Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:35Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:35Z**

review_retry_count=3

**2026-03-01T16:49:35Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:36Z**

decision=retry

**2026-03-01T16:49:36Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:36Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:36Z**

review_retry_count=4

**2026-03-01T16:49:36Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:36Z**

decision=retry

**2026-03-01T16:49:36Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:36Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:36Z**

review_retry_count=5

**2026-03-01T16:49:36Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:36Z**

decision=failed

**2026-03-01T16:49:36Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:36Z**

review_retry_count=5

**2026-03-01T16:49:36Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:36Z**

triage_status=failed

**2026-03-01T16:49:39Z**

decision=retry

**2026-03-01T16:49:39Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:39Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:39Z**

review_retry_count=1

**2026-03-01T16:49:39Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:39Z**

decision=retry

**2026-03-01T16:49:39Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:39Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:39Z**

review_retry_count=2

**2026-03-01T16:49:39Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:39Z**

decision=retry

**2026-03-01T16:49:39Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:39Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:39Z**

review_retry_count=3

**2026-03-01T16:49:39Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:39Z**

decision=retry

**2026-03-01T16:49:39Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:39Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:39Z**

review_retry_count=4

**2026-03-01T16:49:39Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:40Z**

decision=retry

**2026-03-01T16:49:40Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:40Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:40Z**

review_retry_count=5

**2026-03-01T16:49:40Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:40Z**

decision=failed

**2026-03-01T16:49:40Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:40Z**

review_retry_count=5

**2026-03-01T16:49:40Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:40Z**

triage_status=failed

**2026-03-01T16:49:43Z**

decision=retry

**2026-03-01T16:49:43Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:43Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:43Z**

review_retry_count=1

**2026-03-01T16:49:43Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:43Z**

decision=retry

**2026-03-01T16:49:43Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:43Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:43Z**

review_retry_count=2

**2026-03-01T16:49:43Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:43Z**

decision=retry

**2026-03-01T16:49:43Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:43Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:43Z**

review_retry_count=3

**2026-03-01T16:49:43Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:44Z**

decision=retry

**2026-03-01T16:49:44Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:44Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:44Z**

review_retry_count=4

**2026-03-01T16:49:44Z**

triage_reason=review verdict missing explicit pass

**2026-03-01T16:49:44Z**

decision=retry

**2026-03-01T16:49:44Z**

reason=review verdict missing explicit pass

**2026-03-01T16:49:44Z**

review_feedback=review verdict missing explicit pass

**2026-03-01T16:49:44Z**

review_retry_count=5

**2026-03-01T16:49:44Z**

triage_reason=review verdict missing explicit pass
