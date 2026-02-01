---
id: yolo-runner-r5w.12
status: closed
deps: []
links: []
created: 2026-01-18T21:49:22.32158+03:00
type: task
priority: 0
parent: yolo-runner-r5w.14
---
# v1: Command runner abstraction (exec wrapper + fakes)

Introduce a small Go command execution abstraction so unit tests never call real bd/opencode/git.

Files:
- Create: internal/execx/execx.go
- Create: internal/execx/fake.go
- Create: internal/execx/execx_test.go

Rules:
- This task must be implemented in Go
- Do not add new Python files

Acceptance:
- Fake runner records commands (name + args) in call order
- Fake runner can be scripted to return stdout for a specific (name,args)
- Unscripted Output calls fail with a clear missing-stub error
- go test ./... passes

## Acceptance Criteria

- FakeRunner records commands (name + args) in call order
- FakeRunner can be scripted to return stdout bytes for a specific (name,args)
- Unscripted Output calls fail with a clear missing-stub error
- go test ./... covers record + scripted output + missing-stub cases


