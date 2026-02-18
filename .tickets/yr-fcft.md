---
id: yr-fcft
status: closed
deps: [yr-zv9d]
links: []
created: 2026-02-15T17:56:44Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-bs0w
---
# E12-T5 Add actionable diagnostics and machine-readable output

Add remediation-focused messages and machine-readable output mode for config validate.

## Acceptance Criteria

Diagnostics include failing field, reason, remediation, and stable machine-readable schema; strict TDD lifecycle is documented in task notes.

## Notes

**2026-02-16T11:55:00Z**

tdd_red_command=`go test ./cmd/yolo-agent -run 'TestRunConfigValidateCommand' -count=1`
tdd_red_exit=1
tdd_red_result=failed as expected before implementation because new machine-readable schema symbols were undefined.

**2026-02-16T11:55:00Z**

tdd_green_command=`go test ./cmd/yolo-agent -run 'TestRunConfigValidateCommand' -count=1`
tdd_green_exit=0
tdd_green_result=pass after adding actionable diagnostics (field/reason/remediation) and `--format json` stable payload schema for `config validate`.

**2026-02-16T11:55:00Z**

tdd_broader_command=`go test ./cmd/yolo-agent -count=1 && go test ./... -count=1`
tdd_broader_exit=0
tdd_refactor_confirmation=pre-test compile blocker resolved by removing duplicate config command declarations and wiring `runConfigInitCommand` to `defaultRunConfigInitCommand`.
