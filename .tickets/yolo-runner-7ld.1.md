---
id: yolo-runner-7ld.1
status: closed
deps: []
links: []
created: 2026-01-18T21:39:37.386509+03:00
type: task
priority: 1
parent: yolo-runner-7ld
---
# v2: Define core interfaces

Define interfaces for TaskTracker and CodingAgent so runner core stops depending directly on bd/opencode.

## Acceptance Criteria

- Given the v1 runner features, when defining `TaskTracker` and `CodingAgent`, then the interfaces cover every capability v1 uses (select leaf task, show issue fields, update status, close issue, sync; run agent and capture logs)
- Given the runner core package, when building, then it imports only these interfaces (no direct `bd` or `opencode` command construction)
- Given `go test ./...`, when run, then tests validate the interface-driven wiring with fakes


