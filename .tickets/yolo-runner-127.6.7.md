---
id: yolo-runner-127.6.7
status: open
deps: []
links: []
created: 2026-01-20T14:56:05.900342+03:00
type: bug
priority: 2
parent: yolo-runner-127.6
---
# Runner: close-eligible not executed when root has no runnable tasks

## Problem
When the runner is invoked with a root that has no runnable leaf tasks ("empty epic" / empty parent), it exits with "No tasks available" but does not run the end-of-session hygiene step (`bd epic close-eligible`).

This means eligible epics remain open and continue polluting `bd ready` output.

## Repro
- Create/select an epic with no open runnable children
- Run:

```bash
bin/yolo-runner --repo . --root <empty-epic>
```

Observed: runner prints `No tasks available` and exits; eligible epics remain open.

## Expected
Even if no tasks are available under the root, the runner should still run the session-finish hygiene step(s), including:

```bash
bd epic close-eligible
```

(or at least offer to do so).

## Suspected area
Runner loop likely returns early on `no_tasks` and never reaches any end-of-session hook.

## Acceptance
- When root has no runnable tasks, runner still runs `bd epic close-eligible` (or prompts/prints clear instructions)
- Behavior is covered by a unit test for the runner loop/session finalization path
- `go test ./...` passes



