---
id: yolo-runner-127.6.18
status: open
deps: []
links: []
created: 2026-01-27T12:22:04.788655+03:00
type: bug
priority: 2
parent: yolo-runner-127.6
---
# Bug: q prompt to discard changes hangs

## Problem\nAfter pressing , runner asks whether to discard changes, then hangs instead of continuing or exiting.\n\n## Repro\n- Run yolo-runner in TUI mode\n- Press \n- When prompted to discard changes, answer\n- Runner hangs\n\n## Expected\n- After prompt response, runner continues shutdown and exits cleanly\n- No lingering TUI\n\n## Suspected area\nStop-runner prompt handling and cleanup state machine (internal/ui/tui + runner stop flow).

## Acceptance Criteria

- After responding to discard prompt, runner exits without hanging\n- Works for both confirm and cancel paths\n- Bead status restored to open\n- Terminal state restored\n- go test ./... passes


