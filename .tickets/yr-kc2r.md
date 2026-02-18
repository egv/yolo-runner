---
id: yr-kc2r
status: open
deps: [yr-fcft, yr-r3hr]
links: []
created: 2026-02-15T17:56:44Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-bs0w
---
# E12-T7 Document validate/init workflow and troubleshooting

Document command usage, precedence, common failures, and remediation in README/runbook.

## Acceptance Criteria

Docs include validate/init examples and troubleshooting; docs/tests are updated through strict TDD-style doc-test cycle with proof commands in notes.

## Notes

**2026-02-16T08:41:57Z**

tdd_red_command=`go test ./internal/docs -run TestConfigWorkflowDocsCoverValidateInitAndTroubleshooting -count=1`
tdd_red_exit=1
tdd_red_result=failed as expected before docs update (`README missing validate/init workflow guidance`).

**2026-02-16T08:41:57Z**

tdd_green_command=`go test ./internal/docs -run TestConfigWorkflowDocsCoverValidateInitAndTroubleshooting -count=1`
tdd_green_exit=0
tdd_green_result=pass after documenting `yolo-agent config init/validate` workflow in `README.md` and adding `docs/config-workflow.md` troubleshooting runbook.

**2026-02-16T08:41:57Z**

tdd_broader_command=`go test ./internal/docs -count=1`
tdd_broader_exit=0
tdd_refactor_confirmation=no additional refactor required; docs changes are minimal and scoped to workflow/troubleshooting coverage.
