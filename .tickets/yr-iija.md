---
id: yr-iija
status: closed
deps: [yr-09pb, yr-x1rh]
links: []
created: 2026-02-10T08:17:26Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-sbkp
---
# E7-T19 Build status bar metrics and activity indicators

STRICT TDD: implement status bar with runtime, spinner/activity, completed/in-progress/blocked/failed/total counts, queue depth, worker utilization, and event throughput.


## Notes

**2026-02-10T08:19:52Z**

STRICT TDD required. Add failing view/state tests for status bar metrics (runtime, spinner, completed/in-progress/blocked/failed/total, queue depth, utilization, throughput).

**2026-02-10T08:25:05Z**

Error-state design mandatory: status bar must expose aggregated error indicators by scope and severity (run/workers/tasks).
