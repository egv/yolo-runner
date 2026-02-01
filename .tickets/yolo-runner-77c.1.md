---
id: yolo-runner-77c.1
status: closed
deps: []
links: []
created: 2026-01-27T16:28:34.898917+03:00
type: task
priority: 2
parent: yolo-runner-77c
---
# v1.4: Add bubbles spinner component

Introduce a small wrapper around bubbles/spinner for the TUI status bar.

## Acceptance Criteria

- New spinner component uses github.com/charmbracelet/bubbles/spinner
- No custom spinnerFrames in new component
- Unit test covers spinner view output formatting
- go test ./... passes


