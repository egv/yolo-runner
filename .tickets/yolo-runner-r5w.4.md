---
id: yolo-runner-r5w.4
status: closed
deps: [yolo-runner-r5w.3, yolo-runner-r5w.12]
links: []
created: 2026-01-18T21:46:27.82079+03:00
type: task
priority: 0
parent: yolo-runner-r5w.16
---
# v1: Leaf task selection matches Python

Implement leaf-task selection for the Go runner. This must be Go code.

Files:
- Create: internal/runner/select.go
- Create: internal/runner/select_test.go
- Modify (only if needed): internal/beads/client.go

Rules:
- Do not modify beads_yolo_runner.py
- Do not add any new Python files

Acceptance:
- Given a root epic with nested epics, when SelectFirstOpenLeafTaskID is called, then it returns the first open leaf task id by priority
- Given children with missing priority, then missing priority sorts after any numeric priority
- Given non-open children, then they are skipped
- go test ./... passes

## Acceptance Criteria

- Given an epic tree where first open leaf is nested, when SelectFirstOpenLeafTaskID(root) is called, then it returns the correct task id
- Given mixed priorities, when selecting among siblings, then lower numeric priority wins (missing priority sorts last)
- Given non-open statuses, when selecting, then they are skipped
- Given unit tests, when run, then they cover task leaf, epic recursion, empty children, and priority ordering


