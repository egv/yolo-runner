---
id: yolo-runner-127.2
status: closed
deps: [yolo-runner-127.1]
links: []
created: 2026-01-19T11:20:28.034824+03:00
type: epic
priority: 2
parent: yolo-runner-127
---
# v1.2: Console Progress Output

Add human-friendly console output to the Go runner so it is obvious what it is doing and whether it is hung.

Scope:
- Print current task id/title
- Print phase transitions (bd, opencode, git, sync)
- Show a heartbeat/spinner while OpenCode runs

Non-goals:
- No TUI, no curses. Plain stdout/stderr only.

## Acceptance Criteria

- Given the Go runner is executing, it prints which task is being processed and the final outcome (completed/blocked/error)
- Given OpenCode is running, the runner prints periodic heartbeat output so it does not look hung
- Given git/bd operations run, the runner prints phase messages (commit/sync/close)
- go test ./... passes


