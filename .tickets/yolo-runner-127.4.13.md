---
id: yolo-runner-127.4.13
status: closed
deps: []
links: []
created: 2026-01-19T23:03:44.946211+03:00
type: bug
priority: 2
parent: yolo-runner-127.4
---
# TUI last output age always 0s

## Problem
The Bubble Tea TUI status line shows `last output 0s` (or stays at 0s) even while the runner is actively doing work.

Example run:

```bash
export BEADS_NO_DAEMON=1
bin/yolo-runner --repo . --root yolo-runner-127.4
```

Observed output (abbrev):

```
-  -
phase:
last output n/a
```

Expected: `last output` should reflect time since the last meaningful event/output and tick upward over time (or show `n/a` until first event).

## Suspected area
`internal/ui/tui/model.go` updates `lastOutputAt = typed.EmittedAt` for every `runner.Event`. Possible causes:
- The model is not receiving events after startup (so it never gets a non-zero timestamp)
- `EmittedAt` is being set too frequently / incorrectly (e.g. emitted every tick)
- The view rounds to seconds and refresh cadence keeps it pinned at 0s

## Acceptance
- Repro documented
- Root cause identified (event emission cadence and/or timestamps)
- Fix makes `last output` behave as expected
- `go test ./...` passes



