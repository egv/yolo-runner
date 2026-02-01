---
id: yolo-runner-77c.2
status: closed
deps: [yolo-runner-77c.1]
links: []
created: 2026-01-27T16:28:34.978885+03:00
type: task
priority: 2
parent: yolo-runner-77c
---
# v1.4: Use bubbles spinner in TUI model

Replace TUI spinnerFrames with bubbles spinner component in internal/ui/tui/model.go.

## Acceptance Criteria

- TUI status bar uses bubbles spinner.Model
- Spinner advances via spinner.Tick/Update
- Remove spinnerFrames from TUI model
- Update TUI tests accordingly
- go test ./... passes


