---
id: yolo-runner-6tt.1
status: open
deps: []
links: []
created: 2026-01-18T21:39:41.330328+03:00
type: task
priority: 2
parent: yolo-runner-6tt
---
# v3: Define VCS interface

Define a VCS interface (add/status/commit/rev-parse) consumed by runner core.

## Acceptance Criteria

- Given v1 runner needs, when defining VCS interface, then it covers add-all, status-porcelain dirty check, commit with message, and rev-parse HEAD
- Given runner core, when refactored, then it imports only the VCS interface


