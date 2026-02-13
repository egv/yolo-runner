---
id: yr-58fn
status: closed
deps: [yr-70nw]
links: []
created: 2026-02-09T23:07:07Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-s0go
---
# E2-T2 Add backend capability matrix

STRICT TDD: failing tests first. Record capabilities like supports_review and supports_stream.

## Acceptance Criteria

Given backend configs, when selecting backend, then unsupported modes are rejected predictably.


## Notes

**2026-02-13T20:16:09Z**

Implemented backend capability matrix in yolo-agent with supports_review/supports_stream, added selector validation for backend+mode compatibility, and added strict TDD coverage for unknown backend plus unsupported review/stream modes. Validation: go test ./cmd/yolo-agent && go test ./...
