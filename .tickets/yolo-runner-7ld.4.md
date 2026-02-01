---
id: yolo-runner-7ld.4
status: open
deps: []
links: []
created: 2026-01-18T21:39:40.550579+03:00
type: task
priority: 3
parent: yolo-runner-7ld
---
# v2: Add progress web server

Run an embedded HTTP server that serves a simple status page (and/or JSON endpoints) showing runner progress, current task, and recent events.

## Acceptance Criteria

- Given the runner process is running, when I request `GET /api/status`, then it returns JSON with at least: state, active_issue_id, active_issue_title, completed_count, last_error (nullable)
- Given the runner process is running, when I request `GET /`, then it renders an HTML page showing: current state, active issue, and the last N runner events
- Given no auth is configured, when the server is started, then it binds only to localhost by default (v2 local-only)
- Given `go test ./...`, when run, then web server handlers are covered by unit tests


