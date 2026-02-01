---
id: yolo-runner-6tt.2
status: open
deps: []
links: []
created: 2026-01-18T21:39:42.539787+03:00
type: task
priority: 3
parent: yolo-runner-6tt
---
# v3: Git adapter implements VCS

Implement git-backed adapter that satisfies the VCS interface.

## Acceptance Criteria

- Given a Git repository, when calling the Git adapter methods, then they run the equivalent git CLI commands used in v1
- Given unit tests, when run, then they cover dirty detection and commit command construction


