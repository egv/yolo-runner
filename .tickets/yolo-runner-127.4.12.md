---
id: yolo-runner-127.4.12
status: closed
deps: []
links: []
created: 2026-01-19T22:14:36.163844+03:00
type: task
priority: 2
parent: yolo-runner-127.4
---
# v1.2: Document init + agent requirements

Document how agent installation works and how to recover when the runner refuses to start.

Files:
- Modify: README.md

Acceptance:
- README documents yolo-runner init
- README explains why runner refuses to start if agent missing
- README includes troubleshooting steps
- go test ./... passes

## Acceptance Criteria

- README includes init usage and troubleshooting


