---
id: yr-1n7i
status: open
deps: [yr-kruo, yr-qua2]
links: []
created: 2026-02-10T01:46:34Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-2y0b
---
# E7-T10 Add yolo-tui --events-stdin real-time consumer

STRICT TDD: failing tests first. Consume stdin NDJSON incrementally and render live status/heartbeat/output panes.

## Acceptance Criteria

Given piped events from agent stdout, when tui runs with --events-stdin, then UI updates continuously without reading event files.

