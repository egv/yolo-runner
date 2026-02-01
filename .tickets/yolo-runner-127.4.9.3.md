---
id: yolo-runner-127.4.9.3
status: closed
deps: []
links: []
created: 2026-01-19T18:40:25.789243+03:00
type: task
priority: 1
parent: yolo-runner-127.4.9
---
# Align: Document OpenCode config isolation

Document in README why the runner uses an isolated OpenCode config dir and where it lives.

Files:
- Modify: README.md

Acceptance:
- README explains XDG_CONFIG_HOME=~/.config/opencode-runner default
- README explains how to override (if flags added) or how to inspect config
- go test ./... passes

## Acceptance Criteria

- README documents isolated config location
- go test ./... passes


