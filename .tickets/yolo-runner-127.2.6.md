---
id: yolo-runner-127.2.6
status: blocked
deps: []
links: []
created: 2026-01-20T12:31:40.114408+03:00
type: bug
priority: 2
parent: yolo-runner-127.6
---
# Terminal cursor hidden after go runner exits

## Problem
After running `bin/yolo-runner` in a terminal, the cursor sometimes becomes invisible (cursor hidden) even after the process exits. This is disruptive; the terminal looks broken until manually reset.

## Repro
```bash
export BEADS_NO_DAEMON=1
bin/yolo-runner --repo . --root <root>
```
Observed: when the runner exits (success, blocked, or error), the cursor is not visible.

## Expected
Runner should restore terminal state on exit, including making the cursor visible.

## Suspected area
Likely related to terminal control sequences used for in-place updates (CR-based heartbeat/spinner) or Bubble Tea TUI startup/exit. We should ensure we always:
- print a final newline if rendering on one line
- emit the ANSI show-cursor sequence on exit (e.g. `\x1b[?25h`) if we ever hide it
- handle early returns / errors with `defer` cleanup

## Acceptance
- Root cause identified (who hides the cursor: Bubble Tea vs custom progress output)
- Cursor is reliably visible after runner exits across:
  - normal completion
  - blocked task
  - error path
- Add a regression test if feasible (or at least a small manual verification note)



