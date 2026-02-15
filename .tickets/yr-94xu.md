---
id: yr-94xu
status: open
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

