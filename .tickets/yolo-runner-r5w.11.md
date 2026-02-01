---
id: yolo-runner-r5w.11
status: closed
deps: [yolo-runner-r5w.7]
links: []
created: 2026-01-18T21:46:52.573672+03:00
type: task
priority: 3
parent: yolo-runner-r5w.18
---
# v1: End-to-end smoke test instructions

Document a safe manual smoke test for the Go runner.

Files:
- Modify: README.md

Rules:
- No code changes required

Acceptance:
- Includes steps to run on a throwaway branch
- Includes expected log locations
- Includes what success looks like

## Acceptance Criteria

- Given instructions, when followed, then a developer can validate run-once flow end-to-end
- Instructions include safety notes: run on a throwaway branch/worktree, verify `bd ready` selection, and inspect resulting commit
- Document expected log locations and what success looks like


