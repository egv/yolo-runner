---
id: yolo-runner-r5w.8
status: closed
deps: [yolo-runner-r5w.7]
links: []
created: 2026-01-18T21:46:43.668297+03:00
type: task
priority: 2
parent: yolo-runner-r5w.17
---
# v1: Runner loop + --max

Implement runner loop and --max behavior in Go.

Files:
- Modify: internal/runner/runner.go
- Create: internal/runner/loop_test.go

Rules:
- This task must be implemented in Go

Acceptance:
- max=N stops after N completions
- no_tasks stops loop
- go test ./... passes

## Acceptance Criteria

- Given max is set to N, when RunLoop runs and tasks keep completing, then it stops after N completions
- Given RunOnce returns no_tasks, when RunLoop runs, then it stops and returns count
- Given unit tests, when run, then they cover both max-stop and no_tasks-stop behavior


