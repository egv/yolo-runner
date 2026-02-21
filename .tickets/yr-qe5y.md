---
id: yr-qe5y
status: open
deps: [yr-b3z3]
links: []
created: 2026-02-21T20:23:06Z
type: task
priority: -1
assignee: Gennady Evstratov
---
# Design Task Engine Interface

Define the TaskEngine interface and graph types in internal/contracts.

## Description
Create the interface for the graph-based task scheduler and all related types.

## Acceptance Criteria
- Given the interface definition, when reviewed, it includes: BuildGraph, GetNextAvailable, CalculateConcurrency, UpdateTaskStatus, IsComplete
- Given TaskGraph type, when defined, it supports: directed graph with nodes and edges
- Given TaskNode type, when defined, it includes: ID, Task, Status, Parent, Children, Dependencies, Dependents, Depth, Priority
- Given ConcurrencyOptions, when defined, it includes: MaxWorkers, CPUCount, MemoryGB, TaskComplexity

## TDD Protocol
- Write interface definitions first
- Create unit tests for interface contracts
- Document graph algorithms to be used
- Review before implementation

## Dependencies
- Depends on: yr-b3z3

## Links
- Epic: yr-abz7

