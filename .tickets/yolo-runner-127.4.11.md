---
id: yolo-runner-127.4.11
status: closed
deps: []
links: []
created: 2026-01-19T22:14:35.744001+03:00
type: task
priority: 1
parent: yolo-runner-127.4
---
# v1.2: Add init subcommand to install YOLO agent

Add an init command to the Go runner that installs the YOLO agent into the project OpenCode config.

Command:
- yolo-runner init

Behavior:
- Create .opencode/agent/ if needed
- Copy yolo.md -> .opencode/agent/yolo.md
- Overwrite existing .opencode/agent/yolo.md to match yolo.md

Files:
- Modify: cmd/yolo-runner/main.go
- Modify: cmd/yolo-runner/main_test.go
- Modify: internal/opencode/agent.go

Rules:
- Go only

Acceptance:
- Running yolo-runner init creates/overwrites .opencode/agent/yolo.md
- After init, yolo-runner run mode no longer errors on missing agent
- go test ./... passes

## Acceptance Criteria

- init creates/overwrites agent file
- run mode passes agent validation after init
- go test ./... passes


