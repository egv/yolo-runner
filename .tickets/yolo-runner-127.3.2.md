---
id: yolo-runner-127.3.2
status: closed
deps: []
links: []
created: 2026-01-19T15:19:49.67363+03:00
type: task
priority: 1
parent: yolo-runner-127.3
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


