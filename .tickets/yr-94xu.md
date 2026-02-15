---
id: yr-94xu
status: closed
deps: [yr-10ub]
links: []
created: 2026-02-15T17:56:44Z
type: task
priority: 1
assignee: Gennady Evstratov
parent: yr-bs0w
---
# E12-T3 Extract shared config load and semantic validation service

Centralize .yolo-runner/config.yaml load, parse, and semantic validation for reuse by runtime and config validate command.

## Acceptance Criteria

Missing file, invalid YAML, unknown fields, bad durations/numbers, unsupported backend, and profile/auth validation are covered via strict TDD and passing tests are recorded in notes.


## Notes

**2026-02-15T20:14:18Z**

STRICT TDD evidence:
- RED: `go test ./cmd/yolo-agent -run 'TestLoadYoloAgentConfigValidationService|TestYoloAgentConfigValidationService' -count=1` (failed first with undefined `loadYoloAgentConfigValidationService`)
- GREEN: implemented shared `yoloAgentConfigValidationService` and rewired runtime loaders to use it.
- GREEN verification: `go test ./cmd/yolo-agent -run 'TestLoadYoloAgentConfigValidationService|TestYoloAgentConfigValidationService|TestResolveTrackerProfile|TestResolveYoloAgentConfigDefaults' -count=1`
- Regression: `go test ./cmd/yolo-agent -count=1`

Coverage now explicitly includes missing config defaulting, invalid YAML, unknown fields, unsupported backend, invalid duration/number defaults, and profile/auth semantic validation via shared service tests.

Note: full `go test ./...` currently fails on pre-existing timeout in `internal/scheduler` `TestTaskGraphReserveReadyStress_RespectsDAGDependencies` (also reproducible via `go test ./internal/scheduler -run TestTaskGraphReserveReadyStress_RespectsDAGDependencies -count=1 -timeout 2m`).
