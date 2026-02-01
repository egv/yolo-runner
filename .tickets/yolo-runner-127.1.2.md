---
id: yolo-runner-127.1.2
status: closed
deps: []
links: []
created: 2026-01-18T23:31:10.652593+03:00
type: task
priority: 1
parent: yolo-runner-127.1
---
# v1.1: Go runner selection supports molecules

Implement Go selection molecule support.

Files:
- Modify: internal/runner/select.go
- Modify: internal/runner/select_test.go

Rules:
- Must be Go only (no new Python files)
- Do not modify beads_yolo_runner.py in v1.1

Selection semantics:
- Treat issue_type in {"epic","molecule"} as container nodes
- Recurse into container nodes when container status is open OR in_progress
- Leaf tasks are eligible only when status == open

Acceptance:
- Given epic->molecule(open)->task(open), selection returns the task id
- Given epic->molecule(in_progress)->task(open), selection returns the task id
- Given epic->molecule(open, empty)->task(open), selection skips empty molecule and returns the task
- Leaf tasks are selected only when status=open
- go test ./... passes

## Acceptance Criteria

- Given epic->molecule(open)->task(open), selection returns the task id
- Given epic->molecule(in_progress)->task(open), selection returns the task id
- Given epic->molecule(open, empty)->task(open), selection skips empty molecule and returns the task
- Leaf tasks are selected only when status=open
- go test ./... passes


