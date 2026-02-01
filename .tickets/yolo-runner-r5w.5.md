---
id: yolo-runner-r5w.5
status: closed
deps: [yolo-runner-r5w.12]
links: []
created: 2026-01-18T21:46:30.049362+03:00
type: task
priority: 1
parent: yolo-runner-r5w.16
---
# v1: Prompt builder parity

Implement prompt construction in Go that matches the Python runner prompt format.

Files:
- Create: internal/prompt/prompt.go
- Create: internal/prompt/prompt_test.go

Rules:
- This task must be implemented in Go
- Do not add new Python files

Acceptance:
- Prompt includes task header, description section, acceptance section, and strict TDD rules block
- Tests assert key substrings (avoid brittle full-string match)
- go test ./... passes

## Acceptance Criteria

- Given issue id/title/description/acceptance, when BuildPrompt is called, then output includes: task header line, description section, acceptance section, and strict TDD rules
- Given unit tests, when run, then they assert key substrings (avoid brittle full-string match)


