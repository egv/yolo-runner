---
id: yr-r3hr
status: open
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

