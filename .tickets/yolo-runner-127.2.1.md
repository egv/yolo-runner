---
id: yolo-runner-127.2.1
status: closed
deps: []
links: []
created: 2026-01-19T11:20:28.196159+03:00
type: task
priority: 1
parent: yolo-runner-127.2
---
# v1.2: Print task start/end/result

Print minimal lifecycle lines to stdout.

Behavior:
- Maintain a single "current state" string for the run (e.g. selecting task, bd update, opencode running, git commit, bd sync).
- Print at least:
  - Start: "Starting <id>: <title>"
  - State transitions: "State: <state>" (one line per transition)
  - End: "Finished <id>: <result> (<elapsed>)"

Files:
- Modify: cmd/yolo-runner/main.go
- Modify: internal/runner/runner.go

Rules:
- Go only

Acceptance:
- Start line includes issue id and title
- State transitions print a readable state string
- End line includes result and elapsed
- No-tasks case prints message and exits 0
- go test ./... passes

## Acceptance Criteria

- Start line includes issue id and title
- Prints state string on transitions
- End line includes result and elapsed
- No-tasks case prints message
- go test ./... passes


