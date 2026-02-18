---
id: yr-r3hr
status: closed
deps: [yr-94xu]
links: []
created: 2026-02-15T17:56:44Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-bs0w
---
# E12-T6 Add config init command for template generation

Add yolo-agent config init to scaffold starter .yolo-runner/config.yaml.

## Acceptance Criteria

init creates valid starter config, supports safe overwrite semantics, and generated file passes validate; strict TDD with RED->GREEN->REFACTOR recorded in notes.

## Notes

**2026-02-16T08:23:46Z**

TDD RED: Added `TestRunMainConfigInitHelpReturnsZero` in `cmd/yolo-agent/config_init_test.go`, then ran `go test ./cmd/yolo-agent -run TestRunMainConfigInitHelpReturnsZero -count=1` (failed as expected before implementation: `expected exit code 0 for help, got 1`).

**2026-02-16T08:23:46Z**

TDD GREEN: Implemented minimal fix in `cmd/yolo-agent/config_command.go` to treat `flag.ErrHelp` from `config init` parsing as success (`exit 0`), then ran `go test ./cmd/yolo-agent -run TestRunMainConfigInit -count=1` (pass).

**2026-02-16T08:23:46Z**

TDD REFACTOR: Kept logic minimal with no extra behavior changes beyond help-exit handling; ran verification commands `go test ./cmd/yolo-agent -count=1` (pass) and `go test ./... -count=1` (pass).
