---
id: yolo-runner-127.1.1
status: closed
deps: []
links: []
created: 2026-01-18T23:31:08.240732+03:00
type: task
priority: 1
parent: yolo-runner-127.1
---
# v1.1: Selection recurses into molecules

Update Python runner selection logic so issue_type==molecule is treated like issue_type==epic for recursion.

## Acceptance Criteria

- Given a root epic whose first open leaf task is under a molecule, when selecting, then it returns the correct task id
- Given both epic and molecule containers exist, when selecting, then both are traversed
- Given a molecule has no children, when selecting, then it is skipped
- Add tests that fail before the change and pass after


