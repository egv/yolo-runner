---
id: yolo-runner-7ld
status: open
deps: [yolo-runner-r5w]
links: []
created: 2026-01-18T21:38:55.911716+03:00
type: epic
priority: 3
parent: yolo-runner-127
---
# v2: Pluggable task tracker + coding agent + progress web UI

Second iteration: decouple the runner core from specific integrations.\n\n- Task tracker abstraction (Beads becomes an adapter)\n- Coding agent abstraction (OpenCode becomes an adapter)\n- Add a webserver to display current progress, current task, recent events/logs\n\nNon-goals: VCS abstraction.

## Acceptance Criteria

- Given v1 runner behavior, when v2 refactor lands, then core runner code depends only on TaskTracker + CodingAgent interfaces (no direct bd/opencode exec)
- Given Beads and OpenCode adapters, when wired into the runner, then behavior matches v1 (task selection, status transitions, logging, and end-to-end flow)
- Given the web UI is running, when I open it in a browser, then it shows state, active issue (if any), and recent events
- Given the codebase, when I run `go test ./...`, then all tests pass


