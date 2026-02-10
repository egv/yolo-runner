---
id: yr-09pb
status: open
deps: [yr-o4sq]
links: []
created: 2026-02-10T08:17:26Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-sbkp
---
# E7-T17 Implement Elm-style Run->Workers->Tasks state model

STRICT TDD: define immutable-ish derived GUI state model and reducer/update pipeline for stdin events without storing raw event lists.


## Notes

**2026-02-10T08:19:52Z**

STRICT TDD required. Implement Model/Msg/Update/View using Bubble Tea; state hierarchy Run->Workers->Tasks; derived state only.

**2026-02-10T08:25:05Z**

Error-state design mandatory: reducer must model per-scope error states (run-level, worker-level, task-level) with deterministic transitions and no raw-event dependency.
