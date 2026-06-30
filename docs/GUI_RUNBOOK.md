# GUI Production Runbook

## Preflight

- Build binaries: `make build`
- Verify smoke coverage: `make smoke-event-stream`
- Confirm required UI stack dependencies in module graph: Bubble Tea, Bubbles, Lip Gloss
- Ensure stdin stream mode is enabled for agent execution (`--stream`)

## Standard Operator Flow

For queue-backed Startrek or Arc PR operations, prefer the watch supervisor:

```
./bin/yolo-agent watch --repo . --environments ~/.yolo-runner/environments.yaml --tui
```

Use `--events "runner-logs/watch-$(date +%Y%m%d_%H%M%S).events.jsonl"` when the run needs an artifact for review or replay. `watch.tui.default_mode: ui` makes the TUI the config default; `watch.tui.default_mode: stream` keeps NDJSON on stdout unless `--tui` is supplied.

Run the production monitor pipeline over stdin:

```
./bin/yolo-agent --repo . --root <root-id> --model openai/gpt-5.3-codex --stream | ./bin/yolo-tui --events-stdin
```

Shortcut form: `yolo-agent --stream | yolo-tui --events-stdin`.

This single-process pipe is the legacy direct path, and is still the simplest way to watch one run.

## Queue-Split Multi-Process Flow

When debugging the queue-split topology with separate `source` and `runner` processes, there is no single stdout to pipe. Each process appends JSONL to its own `~/.yolo-runner/events/<proc>.jsonl` file. Merge-tail them into the TUI:

```
yolo-agent events follow --since 1h | yolo-tui --events-stdin
```

`events follow` orders events from all process files by timestamp and follows new files as sources/runners start. Events carry `proc` and `item_id` so activity can be grouped per process and per work item. Start it before or after the sources/runners — it picks up files as they appear.

Note: a standalone `runner` currently emits only lifecycle events to its file (rich `agent_text` streaming through the merge-tail is a tracked improvement), so the multi-process TUI shows task/run/landing lifecycle but not yet live agent output.

Expected behavior:

- Status bar updates continuously with runtime, activity, task counters, queue depth, utilization, and throughput
- Hierarchical panels show `Run -> Workers -> Tasks` with scoped severity indicators
- History and panel rendering remain bounded under high event volume

## Failure Handling

- Malformed NDJSON input lines are converted into `decode_error` warnings in rendered output
- Decode warnings are also written to stderr as `event decode warning: ...`
- Stream processing continues unless repeated decode failures exceed safety threshold

## Triage Checklist

- If stream appears stalled, verify upstream producer emits newline-delimited JSON events
- If warning severity remains elevated, inspect recent `decode_error` history lines first
- If panel rows are truncated, adjust viewport/perf controls in monitor model for local diagnostics
