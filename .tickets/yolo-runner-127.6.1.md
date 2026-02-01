---
id: yolo-runner-127.6.1
status: closed
deps: []
links: []
created: 2026-01-20T12:41:35.12426+03:00
type: task
priority: 2
parent: yolo-runner-127.6
---
# Runner: if --root is an open leaf, execute it directly

## Problem
`bin/yolo-runner --root <id>` currently treats `--root` strictly as a *parent container* and calls:

```bash
bd ready --parent <root> --json
```

If `<root>` is itself an open leaf issue (task/bug/etc) with no children, `bd ready --parent <root>` returns `[]` and the runner prints "No tasks available" instead of running `<root>`.

## Desired behavior
If `--root` points to an open leaf issue (i.e., non-container type, status=open), the runner should execute that issue directly.

## Suggested implementation
- Keep current behavior first: call `bd ready --parent <root> --json`.
- If it returns no items, fall back to `bd show <root> --json`.
- If the shown issue is:
  - status=open
  - issue_type is not a container (epic/molecule)
  - then treat it as the selected leaf ID.

## Acceptance
- Given `--root` is a leaf open issue, runner selects and runs it
- Given `--root` is an epic/molecule, runner continues to select an open leaf under it as today
- Given `--root` is closed or blocked, runner prints a clear message and exits 0 (no work)
- `go test ./...` passes; add unit tests for selection logic



