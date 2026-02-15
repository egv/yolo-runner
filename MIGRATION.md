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

## Config Defaults and Precedence (`yolo-agent`)

`yolo-agent` can read defaults from `.yolo-runner/config.yaml` under `agent:`. Runtime selection uses deterministic precedence:

- Backend: `--agent-backend > --backend > YOLO_AGENT_BACKEND > agent.backend > opencode`
- Profile: `--profile > YOLO_PROFILE > default_profile > default`
- Other agent defaults (`agent.model`, `agent.concurrency`, `agent.runner_timeout`, `agent.watchdog_timeout`, `agent.watchdog_interval`, `agent.retry_budget`): CLI flag wins, otherwise config value is used.

Validation is strict for config defaults:

- `agent.concurrency > 0`
- `agent.runner_timeout >= 0`
- `agent.watchdog_timeout > 0`
- `agent.watchdog_interval > 0`
- `agent.retry_budget >= 0`

Invalid values fail startup with field-specific errors that reference `.yolo-runner/config.yaml`.

## Release Gate (E8) Migration

Use the split v2 CLIs for each E8 demo lane, then run the consolidated gate:

- Codex + tk + concurrency=2:
  - `./bin/yolo-agent --repo . --root <tk-root-id> --agent-backend codex --model openai/gpt-5.3-codex --concurrency 2 --stream`
- Claude + conflict retry:
  - `./bin/yolo-agent --repo . --root <tk-root-id> --agent-backend claude --model claude-3-5-sonnet --stream`
- Kimi + Linear profile:
  - `./bin/yolo-agent --repo . --root <linear-project-id> --agent-backend kimi --profile linear-kimi-demo --model kimi-k2 --stream`
- GitHub single-repo profile:
  - `./bin/yolo-agent --repo . --root <github-root-issue-number> --agent-backend codex --profile github-demo --model openai/gpt-5.3-codex --stream`

Run the E8 release checklist in one command:

- `make release-gate-e8`

## Compatibility Behavior

Invoking `yolo-runner` prints a compatibility notice and continues to run, so existing scripts are preserved while migration proceeds.

## Stream Rate Controls

`yolo-agent --stream` now applies output backpressure controls for `runner_output` events by default:

- A bounded coalescing buffer retains up to `--stream-output-buffer` pending output events (default `64`).
- If output arrives faster than `--stream-output-interval` (default `150ms`), intermediate lines are coalesced into the newest emitted line.
- When the buffer overflows, older pending output lines are dropped; emitted events include `metadata.coalesced_outputs` and `metadata.dropped_outputs` counters.

Use `--verbose-stream` to disable coalescing and emit every `runner_output` line.

When `--stream` and `--events <path>` are combined, the file sink runs as a best-effort mirror: stdout NDJSON remains primary, and mirror backpressure/errors do not block live stream delivery.
