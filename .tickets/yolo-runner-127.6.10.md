---
id: yolo-runner-127.6.10
status: closed
deps: []
links: []
created: 2026-01-20T15:02:58.563872+03:00
type: task
priority: 0
parent: yolo-runner-127.6
---
# Selection bug: runner only considers first bd ready item (can miss higher-priority open leaves)

## Problem
Runner selection can miss runnable open tasks because the Beads adapter returns only the *first* element of `bd ready --parent <root> --json`.

If the first returned issue is not runnable (e.g. `status=in_progress`), selection returns `no_tasks` even if later items are `status=open`.

## Evidence
`internal/beads/beads.go`:
- `Ready()` unmarshals the JSON array and then returns `issues[0]` only.

## Expected
On each task transition (each `RunOnce`), the runner should refresh the ready list and select the highest-priority runnable open leaf from the *entire* ready set.

## Suggested fix
- Change `BeadsClient.Ready()` to return the full set (or a synthetic root `Issue{Children: issues}`) and let `SelectFirstOpenLeafTaskID` choose from all children.
- Alternatively, have `Ready()` itself pick the first issue that contains an open runnable leaf.

## Acceptance
- Given `bd ready` returns `[in_progress, open]`, runner selects the `open` leaf
- Runner still honors priority ordering
- Selection is re-evaluated after each task finishes
- `go test ./...` passes with a regression test (fake beads returning multiple ready items)



