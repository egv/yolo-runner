---
id: yr-x22c
status: closed
deps: [yr-oo5l, yr-73gx, yr-nz4x, yr-cjx6, yr-9pjt]
links: []
created: 2026-02-11T13:11:50Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-53vp
---
# Hang: E2 regression run (E2-T1/E2-T2)

Validate the hardening against the real workload that previously hung.

Steps:
- Re-run yolo-agent against the reopened E2 tasks (yr-70nw, yr-58fn) with stream enabled.
- Confirm each runner invocation reaches runner_finished (completed/blocked/failed) and no worker remains stuck.
- If any hang remains, capture:
  - runner-logs/agent.events.jsonl (or streamed NDJSON)
  - per-task runner log + stderr
  - stall classification (if present)
  - a minimal hypothesis for the remaining hang category.

This is a manual validation step with a hard stop on indefinite hangs.

## Acceptance Criteria

Given the reopened E2 tasks,
when running yolo-agent end-to-end,
then the run completes without hanging and emits runner_finished for each started runner.

Given a remaining hang,
when it occurs,
then logs and event metadata are sufficient to classify and open a follow-up ticket.


## Notes

**2026-02-11T13:39:18Z**

Regression validation: go run ./cmd/yolo-agent in clean clone for yr-70nw and yr-58fn emitted runner_started+runner_finished without hangs (stream files: /tmp/yr-70nw.stream.jsonl, /tmp/yr-58fn.stream.jsonl).
