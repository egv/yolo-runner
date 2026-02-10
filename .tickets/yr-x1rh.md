---
id: yr-x1rh
status: closed
deps: [yr-09pb]
links: []
created: 2026-02-10T08:17:26Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-sbkp
---
# E7-T18 Derive runner phases and task summaries

STRICT TDD: map incoming events to derived runner/task phases, counters, timing, warnings/errors, and command summaries for expanded views.


## Notes

**2026-02-10T08:19:52Z**

STRICT TDD required. Add failing reducer tests for phase derivation, command summaries, warnings/errors, counters.

**2026-02-10T08:25:05Z**

Error-state design mandatory: derive and normalize runner/warning/error phases with explicit severity and lifecycle states (active/resolved/terminal).
