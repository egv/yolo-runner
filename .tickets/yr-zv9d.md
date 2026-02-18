---
id: yr-zv9d
status: closed
deps: [yr-n8wy, yr-94xu]
links: []
created: 2026-02-15T17:56:44Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-bs0w
---
# E12-T4 Implement config validate command behavior

Implement yolo-agent config validate to run full config checks and return deterministic exit status.

## Acceptance Criteria

validate exits 0 on valid config and 1 on invalid config with deterministic output; strict TDD evidence includes initial failing tests and final passing suite command in notes.


## Notes

**2026-02-16T08:29:44Z**

tdd_red_command=`go test ./cmd/yolo-agent -run 'TestRunConfigValidateCommand(ValidConfigReturnsZeroWithDeterministicOutput|InvalidConfigReturnsOneWithDeterministicOutput)' -count=1`
tdd_red_exit=1
tdd_red_result=failed as expected before implementation (`config validate command is not implemented` and success-path exit mismatch).

**2026-02-16T08:29:44Z**

tdd_green_command=`go test ./cmd/yolo-agent -run 'TestRunConfigValidateCommand(ValidConfigReturnsZeroWithDeterministicOutput|InvalidConfigReturnsOneWithDeterministicOutput)' -count=1`
tdd_green_exit=0
tdd_green_result=pass after implementing `defaultRunConfigValidateCommand` with deterministic output and shared semantic checks.

**2026-02-16T08:29:44Z**

tdd_broader_command=`go test ./cmd/yolo-agent -count=1 && go test ./... -count=1`
tdd_broader_exit=0
tdd_refactor_confirmation=no additional refactor required; command behavior is minimal and covered by targeted and broader suites.
