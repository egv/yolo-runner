---
id: yolo-runner-127.6.3
status: open
deps: []
links: []
created: 2026-01-20T12:44:51.55441+03:00
type: bug
priority: 2
parent: yolo-runner-127.6
---
# Spinner line: last output sometimes shows double 's'

## Problem
On the console heartbeat/spinner line, the `last output` age sometimes renders with an extra trailing `s` (e.g. `12ss` instead of `12s`).

This makes the output look glitchy and reduces confidence in the status line.

## Repro (rough)
- Run the Go runner while OpenCode runs for a while and watch the spinner line update in-place.

Observed example:
```
phase: opencode_start
last output 176s
\ opencode running - last output 2ss
```

## Expected
`last output` age should be formatted consistently, e.g. `0s`, `12s`, `1m3s`, etc. No duplicated suffix.

## Suspected area
Likely string formatting / concatenation in the console progress reporter (heartbeat/spinner) where a duration string already includes `s` and we append another `s`.

## Acceptance
- Identify the formatting path producing the duplicate `s`
- Fix so `last output` age is always formatted exactly once
- Add/adjust unit test to cover this formatting case
- `go test ./...` passes



