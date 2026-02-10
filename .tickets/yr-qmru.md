---
id: yr-qmru
status: open
deps: [yr-09pb, yr-x1rh]
links: []
created: 2026-02-10T08:17:26Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-sbkp
---
# E7-T20 Add hierarchical worker/task panels with expand-collapse

STRICT TDD: implement scrollable Run->Workers->Tasks UI with collapsed/expanded rows; support both arrows+enter/space and vim keys j/k/h/l.


## Notes

**2026-02-10T08:19:52Z**

STRICT TDD required. Add failing interaction tests first for expand/collapse + navigation (arrow keys and vim keys j/k/h/l + enter/space). Use Bubbles components where appropriate.

**2026-02-10T08:25:05Z**

Error-state design mandatory: expandable panels must surface scoped errors in collapsed and expanded states without ambiguity.
