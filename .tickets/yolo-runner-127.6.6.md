---
id: yolo-runner-127.6.6
status: closed
deps: []
links: []
created: 2026-01-20T13:00:11.260748+03:00
type: task
priority: 2
parent: yolo-runner-127.6
---
# Add stop-runner key (q) with graceful shutdown + cleanup

## Goal
Add an interactive "stop runner" control (key `q`) to gracefully shut down the current run.

## Behavior
When `q` is pressed while the runner is active:
- Stop/terminate the current OpenCode run (cancel context; if needed, kill process)
- Move the currently `in_progress` Beads issue back to `open`
- Restore git working tree to a clean state relative to the last commit **with confirmation**
  - Show what would be discarded (e.g. `git status --porcelain` summary)
  - Ask for explicit confirmation before discarding changes
- Exit the runner cleanly

## UX
- Show the key hint somewhere persistent (status bar / footer): e.g. `q: stop runner`
- After stop is requested, show `Stopping...` state until cleanup finishes.

## Safety constraints
- Never run destructive git operations without explicit confirmation.
- Do not wipe unrelated local changes by default.

## Implementation notes
- Likely needs a Bubble Tea event handler for keypress `q` when running in TUI mode.
- In headless mode, support SIGINT as an equivalent stop path (optional).
- Needs a well-defined cleanup path that can:
  - cancel OpenCode
  - update beads status
  - optionally run git restore/reset upon confirmation

## Acceptance
- Pressing `q` stops the runner within a few seconds
- Beads issue is not left `in_progress`
- Terminal state is restored (cursor visible, newline printed)
- If user declines cleanup, runner exits without discarding changes
- `go test ./...` passes (unit tests for stop state machine; manual steps documented for terminal interaction)



