---
id: yolo-runner-127.3.3
status: closed
deps: []
links: []
created: 2026-01-19T15:19:49.747947+03:00
type: task
priority: 2
parent: yolo-runner-127.3
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


