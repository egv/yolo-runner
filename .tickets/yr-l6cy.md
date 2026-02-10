---
id: yr-l6cy
status: in_progress
deps: [yr-1n7i, yr-51b1, yr-l745]
links: []
created: 2026-02-10T01:46:34Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-2y0b
---
# E7-T13 End-to-end pipe smoke test (agent|tui)

STRICT TDD: create failing e2e first. Verify live pipeline via command piping: yolo-agent --stream | yolo-tui --events-stdin.

## Acceptance Criteria

Given pipe mode, when one task runs, then tui shows live task/runner progress and completion without logfile reads.

