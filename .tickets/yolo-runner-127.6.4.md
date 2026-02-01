---
id: yolo-runner-127.6.4
status: open
deps: []
links: []
created: 2026-01-20T12:46:03.574444+03:00
type: bug
priority: 2
parent: yolo-runner-127.6
---
# Console: duplicate last output indicators with mismatched times

## Problem
The console UI currently shows `last output` in two places, and they sometimes show different values.

This is confusing: users don’t know which one is authoritative.

Observed example:
```
phase: opencode_start
last output 176s
\ opencode running - last output 2ss
```

## Expected
There should be a single `last output` indicator, or if there are two, they must be clearly differentiated and consistent (e.g. "last OpenCode output" vs "last runner event").

## Suspected area
We likely have two different time sources:
- time since OpenCode JSONL file last changed
- time since last runner event / last rendered heartbeat tick

## Acceptance
- Identify both sources and decide the canonical meaning
- Update console rendering so only one `last output` appears (or rename to two explicit labels)
- Add/adjust unit tests for formatting
- `go test ./...` passes



