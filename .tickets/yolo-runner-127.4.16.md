---
id: yolo-runner-127.4.16
status: closed
deps: [yolo-runner-127.4.17]
links: []
created: 2026-01-26T12:38:23.075087+03:00
type: task
priority: 2
parent: yolo-runner-127.4
---
# v1.3: Statusbar for spinner + last output

Move spinner, opencode running, and last-output age into a single status bar line (Bubble Tea).

## Acceptance Criteria

- Spinner + state + last-output age render in single TUI status bar line
- No repeated 'last output' lines in main output
- Status bar updates in place without scrolling
- go test ./... passes

## Notes

Depends on independent spinner


