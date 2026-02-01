---
id: yolo-runner-r5w.3
status: closed
deps: [yolo-runner-r5w.12]
links: []
created: 2026-01-18T21:46:24.949108+03:00
type: task
priority: 1
parent: yolo-runner-r5w.15
---
# v1: Implement Beads CLI adapter

Implement a Go adapter that shells out to bd for: selecting ready children, showing an issue, updating status, closing, syncing.

Files:
- Create: internal/beads/client.go
- Create: internal/beads/client_test.go

Rules:
- This task must be implemented in Go
- Do not add new Python files

Acceptance:
- Parse `bd show <id> --json` array and use element 0
- Parse `bd ready --parent <id> --json` items for id/issue_type/status/priority
- UpdateStatus runs `bd update <id> --status <status>`
- Close runs `bd close <id>`
- Sync runs `bd sync`
- go test ./... passes

## Acceptance Criteria

- Given canned JSON from `bd show <id> --json`, when adapter.Show(id) is called, then it returns id/title/description/acceptance_criteria correctly
- Given canned JSON from `bd ready --parent <id> --json`, when adapter.ReadyChildren(parent) is called, then it returns items with id/issue_type/status/priority
- Given adapter.UpdateStatus(id,status), then it executes `bd update <id> --status <status>`
- Given adapter.Close(id), then it executes `bd close <id>`
- Given adapter.Sync(), then it executes `bd sync`
- Given unit tests, when run, then they validate JSON parsing and error handling for empty arrays/invalid JSON


