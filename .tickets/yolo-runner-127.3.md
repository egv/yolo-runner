---
id: yolo-runner-127.3
status: closed
deps: [yolo-runner-127.1]
links: []
created: 2026-01-19T15:19:49.466284+03:00
type: epic
priority: 2
parent: yolo-runner-127
---
# v1.2: Init Command + YOLO Agent Guard

Add a runner init command that installs the YOLO agent into the project OpenCode configuration, and make the runner refuse to start if the YOLO agent is missing.

Design:
- Project-local (no system-wide install)
- init overwrites .opencode/agent/yolo.md to match yolo.md (source of truth)
- Runner validates agent presence before doing any work

Non-goals:
- Managing other agents
- Editing user global opencode config

## Acceptance Criteria

- Given .opencode/agent/yolo.md is missing, when running yolo-runner (non-init), then it exits non-zero with a clear message and does not modify beads/git
- Given yolo.md exists, when running yolo-runner init, then it creates or overwrites .opencode/agent/yolo.md to match yolo.md
- Given init ran, when running yolo-runner, then it proceeds normally (no agent-not-found)
- go test ./... passes


