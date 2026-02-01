---
id: yolo-runner-r5w.7
status: closed
deps: [yolo-runner-r5w.3, yolo-runner-r5w.4, yolo-runner-r5w.5, yolo-runner-r5w.6, yolo-runner-r5w.9, yolo-runner-r5w.13, yolo-runner-r5w.1, yolo-runner-r5w.2]
links: []
created: 2026-01-18T21:46:41.229348+03:00
type: task
priority: 1
parent: yolo-runner-r5w.16
---
# v1: Runner run-once orchestration

Implement core run-once orchestration in Go that matches beads_yolo_runner.py.

Files:
- Create: internal/runner/runner.go
- Create: internal/runner/runner_test.go
- Modify: cmd/yolo-runner/main.go

Dependencies:
- Uses internal/beads, internal/opencode, internal/vcs/git, internal/prompt, internal/logging via interfaces

Rules:
- This task must be implemented in Go
- Agent name fixed to yolo

Acceptance:
- No tasks -> returns no_tasks
- Dry-run prints selected task + prompt + computed opencode command and makes no mutations
- If opencode yields no git changes -> updates bead to blocked and logs blocked
- If changes -> commit, close bead, verify closed, bd sync
- go test ./... passes

## Acceptance Criteria

- Given no tasks available, when RunOnce is called, then it returns no_tasks
- Given dry-run is true, when RunOnce is called, then it prints task + prompt + computed opencode command and makes no mutations
- Given opencode yields no git changes, when RunOnce completes, then it updates bead to blocked and writes a blocked event
- Given opencode yields changes, when RunOnce completes, then it commits with message `feat: <lower(title)>` (fallback `feat: complete bead task`), closes bead, verifies status==closed, and runs bd sync
- Given unit tests, when run, then they assert command ordering and state transitions using fakes


