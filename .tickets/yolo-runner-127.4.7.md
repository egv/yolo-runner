---
id: yolo-runner-127.4.7
status: closed
deps: []
links: []
created: 2026-01-19T16:12:48.996251+03:00
type: task
priority: 1
parent: yolo-runner-127.4
---
# v1.2: Detect default root (avoid hardcoded algi-8bt)

Fix the Go runner default --root behavior so it does not default to an unrelated project id (algi-8bt).

Goal:
- If --root is omitted, infer a sensible default for this repo OR fail fast with a clear error.

Recommended approach:
- If exactly one roadmap root exists (e.g., top-level epic titled "Roadmap"), default to that id.
- Otherwise, require --root and print instructions.

Files:
- Modify: cmd/yolo-runner/main.go
- Modify: cmd/yolo-runner/main_test.go
- Modify (if needed): internal/beads/client.go

Acceptance:
- Given --root is not provided and a unique "Roadmap" epic exists, runner uses that id
- Given --root is not provided and no unique default can be inferred, runner exits non-zero with a clear message
- Given --root is provided, runner behavior unchanged
- go test ./... passes

## Acceptance Criteria

- Default root is inferred when unique Roadmap epic exists
- Otherwise runner fails fast with clear message
- Explicit --root still works
- go test ./... passes


