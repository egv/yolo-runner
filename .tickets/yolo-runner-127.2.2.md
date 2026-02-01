---
id: yolo-runner-127.2.2
status: closed
deps: []
links: []
created: 2026-01-19T11:20:28.277062+03:00
type: task
priority: 1
parent: yolo-runner-127.2
---
# v1.2: Print phase messages for bd/git operations

Print clear, Claude-Code-style phase messages and command echoes so users see exactly what is happening.

Behavior:
- Before each external command, print: "$ <command>" (single line)
- After the command, print: "ok" or "failed" plus exit code and elapsed time
- Cover at least:
  - bd show/ready/update/close/sync
  - opencode run (show the exact command line)
  - git status/add/commit/rev-parse
  - go test ./... (when invoked by runner, if applicable)

Files:
- Modify: internal/runner/runner.go
- Modify (if needed): internal/opencode/client.go
- Modify (if needed): internal/vcs/git/git.go
- Modify (if needed): internal/beads/client.go

Rules:
- Go only

Acceptance:
- Output contains "$ <command>" line for each command listed above
- Output prints per-command outcome (ok/failed), exit code, and elapsed
- On errors, output shows which command failed
- go test ./... passes

## Acceptance Criteria

- Echoes each external command before running
- Prints ok/failed + exit code + elapsed
- Covers bd/opencode/git commands
- go test ./... passes


