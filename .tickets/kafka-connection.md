---
id: kafka-connection
status: open
deps:
  - kafka-design
links: []
created: 2026-03-01T11:00:00Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: kafka-epic
---
# Task: Implement Kafka Bus - Connection and Configuration

**Epic:** kafka-epic  
**Depends on:** kafka-design

## Description

Implement `KafkaBus` struct with connection management, configuration parsing, and SASL/TLS support using github.com/IBM/sarama.

## Acceptance Criteria

- [ ] `KafkaBus` struct implements `Bus` interface stub (methods return errors initially)
- [ ] `NewKafkaBus(address string, opts ...BusBackendOptions)` constructor connects to Kafka
- [ ] Address parsing supports: `kafka://host:port`, `kafka://host1:port1,host2:port2`
- [ ] SASL support: PLAIN, SCRAM-SHA-256, SCRAM-SHA-512 via connection string query params
- [ ] TLS support: configurable via `tls=true` query param with optional CA/cert/key paths
- [ ] `Close()` cleanly shuts down producers and consumers
- [ ] Unit tests pass with mock sarama client

## TDD Requirement

- Write tests first for: address parsing, SASL config, TLS config, connection success/failure
- Minimum 80% code coverage for connection logic

## Related Files

- `internal/distributed/kafka_bus.go` - Create this file
- `internal/distributed/bus.go` - Bus interface to implement
