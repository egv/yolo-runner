---
id: yolo-runner-r5w
status: closed
deps: []
links: []
created: 2026-01-18T21:38:24.337708+03:00
type: epic
priority: 1
parent: yolo-runner-127
---
# v1: Go standalone runner

Port current Python beads YOLO runner to Go and ship as a single standalone binary.\n\nIncludes: bd integration via CLI, opencode integration via CLI, git integration via CLI, prompt builder, logging, and basic docs/build.\n\nNon-goals: abstraction layers for task tracker/coding agent/VCS; web UI.

## Acceptance Criteria

- Given a repo with bd + opencode installed, when I run `go test ./...`, then tests pass
- Given a repo with Go installed, when I run `go build -o bin/yolo-runner ./cmd/yolo-runner`, then a runnable binary is produced
- Given a configured repo, when I run `bin/yolo-runner --dry-run --repo . --root <id>`, then it prints the selected bead id/title and the exact OpenCode command it would run
- Given an open leaf bead task, when I run the runner in real mode, then it sets status=in_progress, runs OpenCode, commits any changes, closes the bead, verifies it is closed, and runs `bd sync`
- Given OpenCode produces no git changes, when the runner completes, then it updates the bead to status=blocked and writes a blocked entry to runner logs


