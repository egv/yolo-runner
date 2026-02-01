---
id: yolo-runner-127.6.15
status: closed
deps: []
links: []
created: 2026-01-22T11:07:37.399606+03:00
type: bug
priority: 2
parent: yolo-runner-127.6
---
# Ctrl+C does not stop runner in TUI mode

## Problem
When running `bin/yolo-runner` in TUI mode, pressing `Ctrl+C` does not stop the runner. The process continues running and the terminal remains in the UI state.

## Repro
- Start runner in TUI mode:
  ```bash
  export BEADS_NO_DAEMON=1
  ./bin/yolo-runner --repo . --root yolo-runner-127.6
  ```
- Press `Ctrl+C`.
- Observe runner keeps running; no graceful shutdown.

## Expected
`Ctrl+C` should immediately stop the runner (or at least initiate the same graceful shutdown flow as `q`).

## Suspected area
The TUI layer likely captures raw input and does not propagate SIGINT; signal handling is not wired in `cmd/yolo-runner/main.go` / `internal/ui/tui`.

## Acceptance
- `Ctrl+C` triggers stop behavior in TUI mode
- Stops OpenCode, marks current bead back to `open`, exits cleanly
- Terminal state restored



