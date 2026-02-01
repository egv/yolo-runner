---
id: yolo-runner-6tt
status: open
deps: [yolo-runner-7ld]
links: []
created: 2026-01-18T21:38:57.907956+03:00
type: epic
priority: 4
parent: yolo-runner-127
---
# v3: Pluggable VCS

Third iteration: decouple runner from git.\n\n- VCS interface for add/status/commit/rev-parse operations\n- Git adapter implements VCS interface\n- Keep behavior parity with v1\n\nNon-goals: changing default VCS (still git by default).

## Acceptance Criteria

- Given v2 is complete, when v3 lands, then runner core depends only on a VCS interface (no direct git exec)
- Given the Git adapter, when wired in, then behavior matches v1 git operations
- Given `go test ./...`, when run, then all tests pass


