---
id: yolo-runner-127.3.5
status: in_progress
deps: []
links: []
created: 2026-01-30T14:19:35.750960639Z
type: task
priority: 1
parent: yolo-runner-127.3
---
# v1.2: TUI split view (statusbar + scrollable log)

Rework the runner TUI into a two-bubble layout:

- A statusbar bubble glued to the bottom of the screen
- A scrollable textview bubble above it that occupies all remaining space

Behavior change:
- Runner should stop printing log text directly to the terminal
- Instead, append runner output/events to the scrollable textview so all output stays inside the bubble

Acceptance:
- In TUI mode, output is only shown inside the scrollable textview (no raw prints above/beside UI)
- Statusbar remains visible and fixed at bottom while textview scrolls
- Textview supports scrolling through history
- Works on both small and large terminal sizes
- go test ./... passes


