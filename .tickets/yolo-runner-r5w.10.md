---
id: yolo-runner-r5w.10
status: closed
deps: [yolo-runner-r5w.2, yolo-runner-r5w.7]
links: []
created: 2026-01-18T21:46:49.745229+03:00
type: task
priority: 3
parent: yolo-runner-r5w.18
---
# v1: Build artifacts + docs

Add build artifacts and docs for the Go runner.

Files:
- Create: Makefile
- Modify: README.md

Rules:
- Must describe Go build and usage examples

Acceptance:
- make test runs go test ./...
- make build builds bin/yolo-runner
- README documents prerequisites (bd/opencode/git/uv) and example invocations including --model and --dry-run

## Acceptance Criteria

- Given Makefile, when running `make test`, then it runs `go test ./...`
- Given Makefile, when running `make build`, then it builds `bin/yolo-runner`
- Given README, when reading, then it documents prerequisites (bd/opencode/git) and example commands
- Given CI-like environment, when running build/test, then commands succeed


