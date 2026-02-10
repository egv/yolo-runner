---
id: yr-nhop
status: closed
deps: [yr-qua2]
links: []
created: 2026-02-10T01:46:34Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-2y0b
---
# E7-T12 Add optional event file mirror (secondary sink)

STRICT TDD: failing tests first. Keep file sink as optional mirror only, never primary transport for liveness.

## Acceptance Criteria

Given stream mode with file mirror enabled, when events flow, then stdout->stdin path remains primary and file mirror receives same events.

