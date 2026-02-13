---
id: yr-i4ph
status: closed
deps: [yr-yhb9]
links: []
created: 2026-02-09T23:07:07Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-lbx1
---
# E5-T1 Linear auth and profile wiring

STRICT TDD: failing tests first. Use env vars + profile config for Linear credentials.

## Acceptance Criteria

Given missing/invalid Linear auth, when startup runs, then errors are explicit; valid auth passes.


## Notes

**2026-02-13T22:34:31Z**

Implemented Linear auth/profile wiring with startup auth probe. Added internal/linear task manager constructor validation, wired yolo-agent profile env token loading into linear factory with explicit auth errors, and added TDD coverage in cmd/yolo-agent + internal/linear. Validation: go test ./internal/linear ./cmd/yolo-agent -count=1 && go test ./... -count=1. Commit: 69a01fc
