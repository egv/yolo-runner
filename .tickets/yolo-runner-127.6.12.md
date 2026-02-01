---
id: yolo-runner-127.6.12
status: closed
deps: []
links: []
created: 2026-01-22T11:06:14.285495+03:00
type: bug
priority: 2
parent: yolo-runner-127.6
---
# Stop runner: 'Stopping... q: stop runner' shown but run never stops

## Problem
After pressing `q`, the UI shows `Stopping... q: stop runner`, but the runner continues and never exits.

## Repro
- Start `bin/yolo-runner` (TUI mode).
- Press `q`.
- Observe the status line shows `Stopping... q: stop runner` but the process keeps running.

## Expected
Pressing `q` should trigger graceful shutdown:
- OpenCode should be canceled
- current bead moved back to `open`
- runner exits cleanly

## Suspected area
Stop-state wiring between TUI `q` handler, StopState, and OpenCode cancellation (see `internal/ui/tui/model.go`, `internal/runner/stop.go`, `internal/runner/runner.go`).

## Acceptance
- Pressing `q` reliably stops the runner within a few seconds
- No bead left `in_progress`
- Terminal state restored



