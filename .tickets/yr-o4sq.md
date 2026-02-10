---
id: yr-o4sq
status: in_progress
deps: []
links: []
created: 2026-02-10T08:17:26Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-sbkp
---
# E7-T16 Add run params startup stream event

STRICT TDD: add explicit startup event emitted by yolo-agent with initial run parameters (root id, concurrency, model, timeout, stream flags, start time) for GUI initialization.


## Notes

**2026-02-10T08:19:52Z**

STRICT TDD required. Start with failing tests for startup run-params event emission/consumption before implementation.

**2026-02-10T08:25:05Z**

Error-state design mandatory: startup run-params event must include robust validation/error fallback behavior and test cases for malformed/missing startup payloads.
