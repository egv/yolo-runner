---
id: yolo-runner-r5w.1
status: closed
deps: []
links: []
created: 2026-01-18T21:45:23.957149+03:00
type: task
priority: 0
parent: yolo-runner-r5w.14
---
# v1: Mirror YOLO agent into .opencode/agent

Install YOLO agent per-project so opencode can resolve --agent yolo.

Files:
- Create: .opencode/agent/yolo.md (copy from yolo.md)

Rules:
- No system-wide install

Acceptance:
- From repo root, `opencode run "ping" --agent yolo --format json .` does not print agent-not-found
- .opencode/agent/yolo.md is tracked in git

## Acceptance Criteria

- Given the repo has yolo.md, when I create .opencode/agents/yolo.md from it, then the file exists
- Given both files exist, when I compare them, then .opencode/agents/yolo.md content matches yolo.md
- Given the project directory, when I run `opencode run "ping" --agent yolo --format json <project>`, then it does NOT print "agent "yolo" not found" (agent is resolved)
- Given git status, then .opencode/agents/yolo.md is tracked


