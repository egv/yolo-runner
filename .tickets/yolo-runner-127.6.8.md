---
id: yolo-runner-127.6.8
status: open
deps: []
links: []
created: 2026-01-20T14:58:23.181641+03:00
type: bug
priority: 2
parent: yolo-runner-127.6
---
# Stop key (q) does nothing

## Problem
Pressing `q` while the runner is running does not stop the runner / has no effect.

## Expected
A visible keyhint (e.g. in a status bar) and pressing `q` should trigger the stop-runner flow.

## Notes
This is likely because the stop-key feature is not yet implemented/wired into the TUI input loop.

## Acceptance
- `q` key is handled in the UI layer (TUI and/or console mode)
- Pressing `q` initiates graceful shutdown
- Runner does not leave Beads issues `in_progress`
- Terminal state restored


## Notes

-


