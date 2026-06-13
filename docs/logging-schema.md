# Logging Schema

Runner logs written to `runner-logs/*.jsonl` should conform to the structured schema below.

## Required fields

Each log line must include all of these fields:

- `timestamp` (`string`): RFC3339 UTC timestamp (for example, `2026-02-22T10:00:00Z`)
- `level` (`string`): log severity (for example, `info`, `warn`, `error`, `debug`)
- `component` (`string`): logical subsystem that emitted the entry (for example, `runner`, `opencode`)
- `task_id` (`string`): ID of the task/work item
- `run_id` (`string`): ID of the run instance

## Optional fields

Any additional fields are allowed for component-specific payloads, such as:

- `issue_id`
- `request_type`
- `decision`
- `message`
- `request_id`
- `proc`: identifier of the emitting process in a multi-process queue-split run (e.g. `runner-1`, `source-st-adapta`). Written to `~/.yolo-runner/events/<proc>.jsonl` and grouped by `yolo-agent events follow`.
- `item_id`: the queue work-item ID a runner is executing.

## Example lines

```json
{"timestamp":"2026-02-22T10:00:00Z","level":"info","component":"runner","task_id":"task-99","run_id":"run-99","issue_id":"task-99","title":"Fix logging","status":"started"}
{"timestamp":"2026-02-22T10:00:01Z","level":"info","component":"opencode","task_id":"task-99","run_id":"run-99","issue_id":"task-99","request_type":"update","decision":"allow","message":"tool call completed"}
{"timestamp":"2026-02-22T10:00:02Z","level":"info","component":"runner","proc":"runner-1","item_id":"20260222T100002Z-ab12","task_id":"task-99","run_id":"run-99","status":"running"}
```

Use `internal/logging.ValidateStructuredLogLine` to validate generated sample lines in tests.
