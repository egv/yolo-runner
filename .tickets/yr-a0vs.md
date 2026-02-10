---
id: yr-a0vs
status: open
deps: [yr-kruo]
links: []
created: 2026-02-10T01:46:34Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-2y0b
---
# E7-T7 Forward ACP live updates via in-memory event callbacks

STRICT TDD: failing tests first. Wire runner ACP updates to structured live events through callbacks/channels instead of log file tailing.

## Acceptance Criteria

Given active ACP session, when updates arrive, then agent receives structured progress events immediately without reading files.

