---
id: yolo-runner-6tt.3
status: open
deps: []
links: []
created: 2026-01-18T21:39:44.352994+03:00
type: task
priority: 3
parent: yolo-runner-6tt
---
# v3: Runner uses VCS interface

Refactor runner orchestration to call VCS interface rather than git adapter directly.

## Acceptance Criteria

- Given a fake VCS implementation, when runner core is tested, then it can simulate dirty/clean states and assert correct sequencing
- Given the codebase, when searching runner core packages, then there are no direct `git` command invocations


