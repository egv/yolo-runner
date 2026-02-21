---
id: yr-b3z3
status: open
deps: []
links: []
created: 2026-02-21T20:22:56Z
type: task
priority: -1
assignee: Gennady Evstratov
---
# Design Storage Backend Interface

Define the StorageBackend interface and related types in internal/contracts.

## Description
Create the interface abstraction for pluggable storage backends. This is the foundation for the new architecture.

## Acceptance Criteria
- Given the interface definition, when reviewed, it includes: GetTaskTree, GetTask, SetTaskStatus, SetTaskData
- Given TaskTree type, when defined, it contains: Root Task, Tasks map, Relations slice
- Given TaskRelation type, when defined, it supports: parent, depends_on, blocks relation types
- Given the design, when documented, it explains the separation from TaskEngine

## TDD Protocol
- Write interface definitions first
- Create unit tests for interface contracts
- Document expected behavior
- Review before implementation

## Dependencies
None - foundation task

## Links
- Epic: yr-abz7
- ADR: docs/adr/ADR-001-task-subsystem-refactoring.md

