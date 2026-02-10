# GUI Production Runbook

## Preflight

- Build binaries: `make build`
- Verify smoke coverage: `make smoke-event-stream`
- Confirm required UI stack dependencies in module graph: Bubble Tea, Bubbles, Lip Gloss
- Ensure stdin stream mode is enabled for agent execution (`--stream`)

## Standard Operator Flow

Run the production monitor pipeline over stdin:

```
./bin/yolo-agent --repo . --root <root-id> --model openai/gpt-5.3-codex --stream | ./bin/yolo-tui --events-stdin
```

Shortcut form: `yolo-agent --stream | yolo-tui --events-stdin`.

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
