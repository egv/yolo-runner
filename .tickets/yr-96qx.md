---
id: yr-96qx
status: closed
deps: [yr-xl5o]
links: []
created: 2026-02-09T23:07:08Z
type: task
priority: 0
assignee: Gennady Evstratov
parent: yr-4jk8
---
# E6-T1 GitHub auth/profile for single repo

STRICT TDD: failing tests first. Configure single owner/repo scope and token auth.

## Acceptance Criteria

Given GitHub profile, when loaded, then only configured repo is queried and updated.


## Notes

**2026-02-13T23:11:38Z**

Implemented GitHub tracker auth/profile wiring for single-repo mode. Added tracker profile validation for github.scope.owner/github.scope.repo/github.auth.token_env (including single-repo enforcement and token export guidance), buildTaskManager support for github with env token loading and wrapped auth errors, new internal/github TaskManager constructor with GitHub repo auth probe against configured owner/repo, and tests in cmd/yolo-agent/tracker_profile_test.go plus internal/github/task_manager_test.go. Validation: go test ./cmd/yolo-agent -count=1; go test ./internal/github -count=1; go test ./... -count=1.
