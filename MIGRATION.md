# v2 CLI Migration

`yolo-runner` remains available in compatibility mode, but new workflows should use the split v2 CLIs:

- `yolo-agent`: orchestrates task loop execution
- `yolo-task`: task-tracker facade operations
- `yolo-tui`: read-only event monitor for agent/runner logs

## Command Mapping

- Old: `yolo-runner --repo . --root <id> --model <model>`
- New: `yolo-agent --repo . --root <id> --model <model>`

- Old task-state operations via embedded behavior
- New explicit tracker facade:
  - `yolo-task next --root <id>`
  - `yolo-task status --id <task> --status <open|in_progress|blocked|closed|failed>`
  - `yolo-task data --id <task> --set key=value`

- Old TUI behavior in `yolo-runner`
- New read-only monitor: `yolo-agent --stream ... | yolo-tui --events-stdin`

The stdin monitor now tolerates malformed NDJSON lines by emitting `decode_error` warnings and continuing to render subsequent valid events.

## Compatibility Behavior

Invoking `yolo-runner` prints a compatibility notice and continues to run, so existing scripts are preserved while migration proceeds.

## Stream Rate Controls

`yolo-agent --stream` now applies output backpressure controls for `runner_output` events by default:

- A bounded coalescing buffer retains up to `--stream-output-buffer` pending output events (default `64`).
- If output arrives faster than `--stream-output-interval` (default `150ms`), intermediate lines are coalesced into the newest emitted line.
- When the buffer overflows, older pending output lines are dropped; emitted events include `metadata.coalesced_outputs` and `metadata.dropped_outputs` counters.

Use `--verbose-stream` to disable coalescing and emit every `runner_output` line.

When `--stream` and `--events <path>` are combined, the file sink runs as a best-effort mirror: stdout NDJSON remains primary, and mirror backpressure/errors do not block live stream delivery.
