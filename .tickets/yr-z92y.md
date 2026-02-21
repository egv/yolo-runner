---
id: yr-z92y
status: open
deps: [yr-x597]
links: []
created: 2026-02-21T20:25:16Z
type: task
priority: -1
assignee: Gennady Evstratov
---
# Refactor agent loop to use new Task Engine and Storage Backend

Update the agent loop to use StorageBackend and TaskEngine instead of TaskManager.

## Description
Refactor the main agent loop to instantiate and use the new architecture components, replacing the old TaskManager usage.

## Acceptance Criteria
- Given agent starts, when initialized, uses StorageBackend to fetch tasks
- Given tasks available, when processing, uses TaskEngine to determine next tasks
- Given task completes, when updating status, uses StorageBackend.SetTaskStatus
- Given concurrent limit, when spawning workers, uses TaskEngine.CalculateConcurrency
- Given all tasks complete, when loop ends, uses TaskEngine.IsComplete
- Given configuration, when profile selected, correct StorageBackend instantiated

## TDD Protocol
- Update existing tests to use new interfaces
- Ensure all existing tests pass
- Add new tests for integration points
- Refactor while keeping tests green

## Dependencies
- Depends on: yr-x597

## Links
- Epic: yr-abz7

