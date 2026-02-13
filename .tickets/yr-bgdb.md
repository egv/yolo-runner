---
id: yr-bgdb
status: closed
deps: [yr-ibb3, yr-nnog]
links: []
created: 2026-02-09T23:07:08Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-5543
---
# E8-T4 Self-host demo: GitHub single-repo

STRICT TDD: add acceptance test then run GitHub single-repo flow.

## Acceptance Criteria

Given github profile, when run completes, then at least one issue closes end-to-end.


## Notes

**2026-02-13T23:42:01Z**

Implemented strict TDD acceptance hardening for GitHub single-repo self-host flow in cmd/yolo-agent/e2e_test.go. Strengthened TestE2E_GitHubProfileProcessesAndClosesIssue to use the real Codex CLI adapter (fake codex binary) instead of fake runner, and added assertions for run_started backend=codex, runner_finished backend=codex + review_verdict=pass, isolated clone_path usage, and final GitHub issue closure with open->closed transitions. Failing-first evidence: go test ./cmd/yolo-agent -run TestE2E_GitHubProfileProcessesAndClosesIssue -count=1 initially failed on missing runner_finished backend metadata, then passed after implementation. Validation: go test ./cmd/yolo-agent -run TestE2E_GitHubProfileProcessesAndClosesIssue -count=1; go test ./cmd/yolo-agent -count=1; go test ./... -count=1.
