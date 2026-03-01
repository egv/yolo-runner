---
id: kafka-config
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
# Task: Update Configuration and CLI for Kafka Backend

**Epic:** kafka-epic  
**Depends on:** kafka-connection

## Description

Add `kafka` as a valid backend option in config and CLI, update factory function.

## Acceptance Criteria

- [ ] `DistributedBusConfig.Backend` accepts `"kafka"`
- [ ] CLI flags `--distributed-bus-backend kafka` works
- [ ] Config file `distributed_bus.backend: kafka` parsed correctly
- [ ] `newDistributedBus()` factory in all cmd/* main.go files handles `kafka`
- [ ] Updated `cmd/yolo-agent/main.go`, `cmd/yolo-runner/main.go`, `cmd/yolo-tui/main.go`, `cmd/yolo-webui/main.go`
- [ ] Unit tests for config parsing with kafka backend pass

## TDD Requirement

- Write config parsing tests first for kafka backend
- Test factory function with kafka backend selection

## Related Files

- `cmd/yolo-agent/main.go` - Update newDistributedBus
- `cmd/yolo-runner/main.go` - Update newDistributedBus
- `cmd/yolo-tui/main.go` - Update newDistributedBus
- `cmd/yolo-webui/main.go` - Update newDistributedBus
- `internal/distributed/config.go` - Update config parsing
