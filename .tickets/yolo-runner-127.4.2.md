---
id: yolo-runner-127.4.2
status: closed
deps: []
links: []
created: 2026-01-19T15:55:27.275855+03:00
type: task
priority: 1
parent: yolo-runner-127.4
---
# v1.2: TUI model for runner status

Implement a Bubble Tea model that renders:
- current task (id + title)
- current phase/state string
- spinner that advances when new output is detected
- last-output age seconds

Files:
- Create: internal/ui/tui/model.go
- Create: internal/ui/tui/model_test.go

Acceptance:
- Given status updates, view output contains task id/title and phase string
- Spinner advances when notified of new output
- Tests run without requiring a real TTY
- go test ./... passes

## Acceptance Criteria

- Renders task and phase
- Spinner advances on output events
- Tests are deterministic and non-TTY
- go test ./... passes


