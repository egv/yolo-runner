---
id: yolo-runner-127.6.9
status: open
deps: []
links: []
created: 2026-01-20T14:58:26.535487+03:00
type: bug
priority: 2
parent: yolo-runner-127.6
---
# Missing [x/y] progress indicator in runner UI

## Problem
Runner UI does not show the `[x/y]` progress indicator (executed/remaining tasks) next to the current task.

## Expected
Show `[x/y]` (scope progress) and update it each time the runner advances to the next task.

## Notes
This is currently only tracked as a nice-to-have task; converting to a bug to ensure it isn't forgotten.

## Acceptance
- `[x/y]` appears next to the current task ID/title
- `y` reflects total runnable leaves in scope; `x` increments per finished leaf
- Updates on each task transition



