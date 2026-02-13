---
id: yr-iedw
status: closed
deps: [yr-w66o, yr-wiur, yr-bgdb]
links: []
created: 2026-02-09T23:07:08Z
type: task
priority: 2
assignee: Gennady Evstratov
parent: yr-5543
---
# E8-T5 Release gate and migration updates

STRICT TDD: add docs/tests validating release gate checklist and updated migration instructions.

## Acceptance Criteria

Given completed demos, when release gate runs, then checklist passes and docs are updated.


## Notes

**2026-02-13T23:53:52Z**

Implemented strict TDD release-gate updates. Added internal/docs/release_gate_test.go with failing-first contract tests for Makefile/README/MIGRATION release-gate checklist coverage; added Makefile target release-gate-e8 running E8 acceptance tests plus docs checks; documented E8 Release Gate Checklist in README and Release Gate (E8) Migration commands in MIGRATION. Validation: go test ./internal/docs -run 'ReleaseGate|E8' -count=1; go test ./internal/docs -count=1; go test ./cmd/yolo-agent -run 'TestE2E_(CodexTKConcurrency2LandsViaMergeQueue|ClaudeConflictRetryPathFinalizesWithLandingOrBlockedTriage|KimiLinearProfileProcessesAndClosesIssue|GitHubProfileProcessesAndClosesIssue)$' -count=1; make release-gate-e8; go test ./... -count=1. Commit: ba7ffa6.
