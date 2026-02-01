---
id: yolo-runner-127.5
status: closed
deps: []
links: []
created: 2026-01-20T11:03:44.744101+03:00
type: task
priority: 2
parent: yolo-runner-127
---
# Runner workflow: run bd epic close-eligible to reduce bd ready noise

## Problem
As we complete tasks, their parent epics can remain open even when all children are done. This makes `bd ready` output noisy and can cause the runner (and humans) to see irrelevant open epics.

## Proposal
After finishing a batch of tasks / at end of a runner session, run:

```bash
bd epic close-eligible
```

This closes epics whose children are all complete.

## Acceptance
- Document this as a standard step in the runner workflow (AGENTS.md or README, whichever is appropriate)
- If the runner has an explicit "session end" path, consider invoking `bd epic close-eligible` there (optional)
- Ensure `bd ready` output is cleaner in practice



