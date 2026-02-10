---
id: yr-kruo
status: closed
deps: [yr-c5hx]
links: []
created: 2026-02-10T01:46:34Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-2y0b
---
# E7-T6 Define live stream transport contract (stdout->stdin)

STRICT TDD: failing tests first. Define NDJSON event stream contract for yolo-agent stdout and yolo-tui stdin with clear framing, ordering, and backward compatibility.

## Acceptance Criteria

Given streamed events, when parsed by tui, then events decode incrementally from stdin without logfile dependency.

