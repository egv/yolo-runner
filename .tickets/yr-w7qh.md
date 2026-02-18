---
id: yr-w7qh
status: closed
deps: [yr-9es9, yr-bho4, yr-4qc2]
links: []
created: 2026-02-10T00:15:30Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-qxrw
---
# E10-T15 Implement dedicated Linear session worker entrypoint

STRICT TDD: failing tests first. Add worker binary/entrypoint that consumes queued session jobs and invokes runner/agent execution flow outside webhook request lifecycle.

## Acceptance Criteria

Given queued session jobs, when worker runs, then task execution and activity emission occur asynchronously without blocking webhook server.


## Notes

**2026-02-18T20:24:51Z**

review_fail_feedback=`cmd/yolo-linear-worker/main.go:23` leaves `processLinearSessionJob` as a no-op, so the worker consumes queue entries without invoking runner/agent execution or emitting Linear activities; implement real job processing wiring and add/adjust tests to prove queued `created`/`prompted` jobs execute asynchronously outside webhook request handling.

**2026-02-18T20:24:51Z**

review_feedback=`cmd/yolo-linear-worker/main.go:23` leaves `processLinearSessionJob` as a no-op, so the worker consumes queue entries without invoking runner/agent execution or emitting Linear activities; implement real job processing wiring and add/adjust tests to prove queued `created`/`prompted` jobs execute asynchronously outside webhook request handling.

**2026-02-18T20:24:51Z**

review_retry_count=1

**2026-02-18T20:24:51Z**

review_verdict=fail
