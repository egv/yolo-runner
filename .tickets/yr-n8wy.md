---
id: yr-n8wy
status: closed
deps: [yr-10ub]
links: []
created: 2026-02-15T17:56:44Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-bs0w
---
# E12-T2 Add CLI routing for config subcommands

Add yolo-agent config command routing for validate/init while preserving existing run behavior.

## Acceptance Criteria

Given parser tests, config subcommands route correctly and legacy run path remains backward compatible; implementation follows strict TDD (RED->GREEN->REFACTOR) with test evidence in notes.


## Notes

**2026-02-15T20:02:24Z**

STRICT TDD evidence:
- RED: `go test ./cmd/yolo-agent -run 'TestRunMainRoutesConfig|TestRunMainRejectsUnknownConfigSubcommand' -count=1` failed at compile stage with undefined router symbols (`runMainWithConfigHandlers`, `configCommandHandlers`, `configCommandRequest`).
- GREEN (targeted): `go test ./cmd/yolo-agent -run 'TestRunMainRoutesConfig|TestRunMainRejectsUnknownConfigSubcommand' -count=1`.
- GREEN (package): `go test ./cmd/yolo-agent -count=1`.
- GREEN (full regression): `go test ./... -count=1`.

Implemented parser routing for `yolo-agent config validate|init` with backward-compatible legacy run flow and parser-level coverage for both direct subcommand and global-flag-prefixed forms.
