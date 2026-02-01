---
id: yolo-runner-127.4.18
status: blocked
deps: []
links: []
created: 2026-01-26T12:38:23.231066+03:00
type: task
priority: 2
parent: yolo-runner-127.4
---
# v1.3: Route bd/command output to logs

Send bd/git command outputs to log files; TUI should only show high-level actions (e.g., 'getting task info', 'closing task').

## Acceptance Criteria

- bd/git command stdout/stderr routed to log files
- TUI/headless only shows high-level action labels (e.g., 'getting task info')
- No command echo in TUI output
- go test ./... passes

## Notes

Focus on output hygiene


