---
id: yr-qua2
status: open
deps: [yr-kruo, yr-tilv]
links: []
created: 2026-02-10T01:46:34Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-2y0b
---
# E7-T9 Add yolo-agent --stream mode (JSON stdout, human stderr)

STRICT TDD: failing tests first. Add stream mode where stdout is NDJSON events only; diagnostics remain on stderr.

## Acceptance Criteria

Given --stream mode, when agent runs, then stdout contains valid NDJSON only and stderr keeps human diagnostics.

