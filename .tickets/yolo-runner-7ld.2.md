---
id: yolo-runner-7ld.2
status: open
deps: []
links: []
created: 2026-01-18T21:39:38.071258+03:00
type: task
priority: 2
parent: yolo-runner-7ld
---
# v2: Beads adapter implements TaskTracker

Implement Beads-backed TaskTracker using bd CLI (or direct API later), matching v1 behavior.

## Acceptance Criteria

- Given a Beads-backed repository, when TaskTracker.SelectNextLeafTask(root) is called, then it returns the same leaf selection as `bd ready --parent <root> --json` recursion in v1
- Given a bead id, when TaskTracker.GetIssue(id) is called, then it returns title, description, and acceptance criteria as used in the prompt
- Given a bead id and status, when TaskTracker.UpdateStatus is called, then it runs the equivalent `bd update <id> --status <status>`
- Given a bead id, when TaskTracker.Close is called, then it runs `bd close <id>` and verifies closed via `bd show`
- Given unit tests, when run, then they cover JSON parsing, empty/invalid outputs, and command errors


