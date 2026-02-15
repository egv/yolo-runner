---
id: yr-iyka
status: open
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

