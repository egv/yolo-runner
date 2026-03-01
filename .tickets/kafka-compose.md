---
id: kafka-compose
status: open
deps: []
links: []
created: 2026-03-01T11:00:00Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: kafka-epic
---
# Task: Add Podman Compose for Kafka Development

**Epic:** kafka-epic  
**Depends on:** None

## Description

Create `compose.kafka.yaml` with Kafka + Zookeeper (or KRaft mode) for local development and CI testing using Podman.

## Acceptance Criteria

- [ ] `compose.kafka.yaml` defines Kafka 3.x + Zookeeper OR KRaft controller
- [ ] Kafka accessible at `localhost:9092`
- [ ] Includes healthcheck for Kafka readiness
- [ ] Makefile target `kafka-up` starts stack with `podman-compose`, `kafka-down` stops it
- [ ] Documentation in compose file comments explains usage
- [ ] Tested: `make kafka-up && go test -tags=integration ./internal/distributed/...`

## TDD Requirement

N/A (infrastructure task)

## Related Files

- `compose.kafka.yaml` - Create this file (podman-compose compatible)
- `Makefile` - Add kafka-up/kafka-down targets using podman-compose
