---
id: yolo-runner-127.6.14
status: closed
deps: []
links: []
created: 2026-01-22T11:06:30.954496+03:00
type: bug
priority: 2
parent: yolo-runner-127.6
---
# Quit hint appears only after pressing q

## Problem
The UI hint `press q to quit` (or `q: stop runner`) is not shown until after pressing `q`.

## Repro
- Start `bin/yolo-runner` in TUI mode.
- Observe that the quit hint is absent.
- Press `q`.
- The hint appears only after the stop is already requested.

## Expected
The quit hint should be visible from the start of the run (status bar/footer), so users know how to stop the runner.

## Suspected area
TUI status line rendering / footer state in `internal/ui/tui/model.go`.

## Acceptance
- Quit hint visible immediately on run start
- Still visible while stopping
- `go test ./...` passes



