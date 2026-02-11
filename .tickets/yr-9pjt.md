---
id: yr-9pjt
status: closed
deps: [yr-7s41]
links: []
created: 2026-02-11T13:11:50Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-53vp
---
# Hang: Emit richer triage metadata in events

Improve observability so hangs can be diagnosed from yolo-tui / agent.events.jsonl alone.

Add metadata to runner events:
- runner_started: log_path, clone_path, mode, model, backend, start timestamp.
- runner_finished: status, reason, stall category (if any), sessionID (if extractable), last_output_age.

Ensure StreamEventSink/FileEventSink preserve this metadata and tests cover the JSON shape.

## Acceptance Criteria

Given a stalled run,
when viewing the event stream,
then I can see log_path + clone_path and a classified reason without opening additional files.

Given the event sinks,
when emitting runner events,
then metadata is preserved in JSONL output.


## Notes

**2026-02-11T13:39:18Z**

Completed: runner_started/runner_finished now emit rich metadata (log_path/mode/model/backend/status/reason/stall diagnostics) with test coverage.
