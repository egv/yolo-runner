---
id: kafka-epic
status: open
deps: []
links: []
created: 2026-03-01T11:00:00Z
type: epic
priority: 0
assignee: Gennady Evstratov
---
# V3.0: Add Apache Kafka as Event Bus Backend

This epic tracks the implementation of Apache Kafka as a third option for the distributed event bus, alongside Redis and NATS.

## Technical Decisions

- **Kafka Library:** github.com/IBM/sarama (pure Go, no CGO)
- **Auth Support:** Full SASL/TLS from the start (PLAIN, SCRAM-SHA-256, SCRAM-SHA-512, TLS)
- **Dev Setup:** Podman Compose with Kafka for local testing

## Implementation Overview

### Topic Naming Convention
- Pub/Sub: `{prefix}.{subject}`
- Queues: `{prefix}.queue.{queue_name}`

### Consumer Strategy
- Consumer groups from `BusBackendOptions.Group`
- Partition by `IdempotencyKey` for ordering
- Manual offset commit on Ack

## Tasks and Dependencies

### Phase 1: Foundation (Parallel)
- kafka-design - Design Kafka Topic Schema and Consumer Strategy
- kafka-compose - Add Podman Compose for Kafka Development

### Phase 2: Core Implementation (After kafka-design)
- kafka-connection - Implement Kafka Bus - Connection and Configuration
  - Blocked by: kafka-design
- kafka-pubsub - Implement Kafka Bus - Publish and Subscribe
  - Blocked by: kafka-connection
- kafka-queue - Implement Kafka Bus - Queue (Enqueue and ConsumeQueue)
  - Blocked by: kafka-connection
- kafka-requestreply - Implement Kafka Bus - Request/Reply Pattern
  - Blocked by: kafka-pubsub

### Phase 3: Testing (After Core Implementation)
- kafka-contract - Add Kafka Bus Contract Tests
  - Blocked by: kafka-pubsub, kafka-queue, kafka-requestreply
- kafka-integration - Add Kafka Bus Integration Tests
  - Blocked by: kafka-contract

### Phase 4: Integration (After Testing)
- kafka-config - Update Configuration and CLI for Kafka Backend
  - Blocked by: kafka-connection
- kafka-smoke - Add Kafka Smoke Test and E2E Test
  - Blocked by: kafka-integration, kafka-config
- kafka-docs - Documentation for Kafka Backend
  - Blocked by: kafka-config

## Key Files to Create/Modify

- `internal/distributed/kafka_bus.go` - New Kafka Bus implementation
- `internal/distributed/kafka_bus_test.go` - Unit tests with mocks
- `internal/distributed/kafka_bus_integration_test.go` - Integration tests
- `internal/distributed/kafka_smoke_test.go` - Smoke tests
- `compose.kafka.yaml` - Podman Compose for local Kafka
- `cmd/*/main.go` - Update factory functions for kafka backend
- `internal/distributed/config.go` - Update config parsing

## Acceptance Criteria (Epic Level)

- [ ] All Bus interface methods work with Kafka backend
- [ ] Contract tests pass for KafkaBus
- [ ] Integration tests pass with real Kafka
- [ ] CLI accepts `--distributed-bus-backend kafka`
- [ ] Config file accepts `distributed_bus.backend: kafka`
- [ ] Documentation includes Kafka setup guide
