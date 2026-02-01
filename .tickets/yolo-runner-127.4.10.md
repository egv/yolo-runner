---
id: yolo-runner-127.4.10
status: closed
deps: []
links: []
created: 2026-01-19T22:14:35.318898+03:00
type: task
priority: 1
parent: yolo-runner-127.4
---
# v1.2: Refuse to run if YOLO agent missing

Before selecting any beads task, validate the YOLO agent is installed for this repo.

Files:
- Modify: cmd/yolo-runner/main.go
- Create: internal/opencode/agent.go
- Create: internal/opencode/agent_test.go

Rules:
- Go only

Acceptance:
- If .opencode/agent/yolo.md does not exist, runner exits non-zero and prints a clear message
- If agent file exists but does not contain frontmatter key permission: allow, runner exits non-zero and prints guidance to run init
- Validation happens before any bd update / git add / opencode run
- go test ./... passes

## Acceptance Criteria

- Missing agent file fails fast with clear message
- Missing permission allow fails fast with guidance
- Validation runs before any mutations
- go test ./... passes


