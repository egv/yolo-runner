---
id: yolo-runner-127.6.5
status: closed
deps: []
links: []
created: 2026-01-20T12:54:22.711591+03:00
type: task
priority: 2
parent: yolo-runner-127.6
---
# Nice-to-have: show progress [x/y] tasks executed/remaining

## Goal
Show a compact progress counter like `[x/y]` next to the active task title and refresh it every time the runner moves to the next task.

Example:
- `Starting [3/12] yolo-runner-127.2.3: OpenCode heartbeat while logs grow`

## Definition of y
`y` should be the total number of leaf issues that the runner intends to execute for the chosen root (the "scope"):
- computed once at the start of the run (or recomputed on each selection if scope can change)
- includes all runnable leaf types (task/bug/feature/etc) under the root, excluding container nodes (epic/molecule)

`x` increments each time the runner finishes a leaf (completed or blocked), and resets to 0 for a new process invocation.

## Implementation notes
- Add a Beads API to fetch the full tree (currently `bd ready` returns only the first ready root; may need `bd show <root> --json` or `bd list --parent <root> --tree --json` if available).
- Add a small helper to count runnable leaves in the tree.
- Ensure output stays compact (do not print issue descriptions).

## Acceptance
- On startup, runner computes `y` for the selected root scope
- Start line includes `[x/y]` for the active task
- Counter updates when moving to the next task
- Works for roots that are epics and for roots that are leaf issues (once leaf-root support is implemented)
- `go test ./...` passes with deterministic tests for counting and formatting



