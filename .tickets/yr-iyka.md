---
id: yr-iyka
status: closed
deps: [yr-fcft, yr-kc2r]
links: []
created: 2026-02-15T17:56:44Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-bs0w
---
# E12-T8 Add e2e and CI coverage for config commands

Add end-to-end tests and CI target to prevent regressions in validate/init commands.

## Acceptance Criteria

CI covers happy and failure paths (missing file, invalid values, missing auth env); strict TDD cycle and passing CI/test commands are recorded in notes.

## Notes

**2026-02-18T08:30:07Z**

tdd_red_command=`go test ./internal/docs -run 'TestMakefileHas(AgentTUISmokeTarget|ConfigCommandSmokeTarget)' -count=1 && go test ./cmd/yolo-agent -run 'TestE2E_ConfigCommands_' -count=1`
tdd_red_exit=1
tdd_red_result=failed as expected before Makefile wiring (`smoke-config-commands` target missing and smoke flow did not include config-command coverage).

**2026-02-18T08:30:07Z**

tdd_green_command=`go test ./cmd/yolo-agent -run 'TestE2E_ConfigCommands_' -count=1 && go test ./internal/docs -run 'TestMakefileHas(AgentTUISmokeTarget|ConfigCommandSmokeTarget|EventStreamSmokeTarget)' -count=1`
tdd_green_exit=0
tdd_green_result=pass after adding config-command E2E tests and wiring `smoke-config-commands` into `smoke-agent-tui` in `Makefile`.

**2026-02-18T08:30:07Z**

tdd_broader_command=`go test ./cmd/yolo-agent -count=1 && go test ./internal/docs -count=1 && go test ./... -count=1`
tdd_broader_exit=0
tdd_refactor_confirmation=no additional refactor required; coverage is added via focused E2E tests and Makefile CI contracts.
