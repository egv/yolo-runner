---
id: yolo-runner-r5w.6
status: closed
deps: [yolo-runner-r5w.12]
links: []
created: 2026-01-18T21:46:37.162841+03:00
type: task
priority: 1
parent: yolo-runner-r5w.15
---
# v1: Git CLI adapter (v1-specific)

Implement the Git adapter for the Go runner (git-specific for v1). This must be Go code.

Files:
- Create: internal/vcs/git/git.go
- Create: internal/vcs/git/git_test.go

Rules:
- Do not add any new Python files

Acceptance:
- IsDirty uses `git status --porcelain` and returns true iff output is non-empty
- AddAll runs `git add .`
- Commit runs `git commit -m <msg>`
- RevParseHead runs `git rev-parse HEAD`
- Unit tests validate command construction using a fake command runner
- go test ./... passes

## Acceptance Criteria

- Given porcelain output is empty, when IsDirty is called, then it returns false
- Given porcelain output is non-empty, when IsDirty is called, then it returns true
- Given Commit(msg) is called, then it runs `git commit -m <msg>`
- Given RevParseHead is called, then it runs `git rev-parse HEAD`
- Given unit tests, when run, then they cover dirty/clean detection and error propagation


