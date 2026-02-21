---
id: yr-ho6u
parent: yr-abz7
status: open
deps: [yr-qe5y, yr-io10]
links: []
created: 2026-02-21T20:23:59Z
type: task
priority: -1
assignee: Gennady Evstratov
---
# Write tests for Task Engine graph operations

Create comprehensive unit tests for TaskEngine before implementation.

## Description
Following strict TDD, write tests for graph building, dependency resolution, and concurrency calculation.

## Acceptance Criteria
- Given sample TaskTree, when BuildGraph called, returns valid TaskGraph with all nodes
- Given graph with dependencies, when GetNextAvailable called, returns only tasks with satisfied dependencies
- Given complex task tree, when CalculateConcurrency called, returns max parallelizable task count
- Given graph with circular dependency, when BuildGraph called, returns error with cycle path
- Given updated task status, when UpdateTaskStatus called, dependent tasks become available
- Given all tasks completed, when IsComplete called, returns true

## TDD Protocol
- Write all failing tests first
- Create test cases for: linear chain, diamond pattern, fan-out, fan-in, circular
- Test concurrency calculation with various graph shapes
- Tests must fail before implementation exists

## Dependencies
- Depends on: yr-qe5y
- Depends on: yr-io10

## Links
- Epic: yr-abz7

## TDD Evidence
- RED run recorded on 2026-02-21 22:07:26 UTC.
- Command:
  - `go test ./internal/scheduler -run 'TestTaskGraph(BuildGraphGetNextAvailableUpdateTaskStatus_Topologies|CalculateConcurrencyAcrossTopologies|IsCompleteReturnsTrueWhenAllTasksFinished|RejectsCircularDependenciesWithCyclePath)$' -count=1`
- Output (failed as expected before Task Engine implementation):
```text
--- FAIL: TestTaskGraphRejectsCircularDependenciesWithCyclePath (0.00s)
    graph_test.go:269: expected circular dependency error, got nil
--- FAIL: TestTaskGraphCalculateConcurrencyAcrossTopologies (0.00s)
    --- FAIL: TestTaskGraphCalculateConcurrencyAcrossTopologies/empty_graph (0.00s)
        graph_test.go:445: TaskGraph.CalculateConcurrency is not implemented
    --- FAIL: TestTaskGraphCalculateConcurrencyAcrossTopologies/linear_chain (0.00s)
        graph_test.go:445: TaskGraph.CalculateConcurrency is not implemented
    --- FAIL: TestTaskGraphCalculateConcurrencyAcrossTopologies/diamond (0.00s)
        graph_test.go:445: TaskGraph.CalculateConcurrency is not implemented
    --- FAIL: TestTaskGraphCalculateConcurrencyAcrossTopologies/fan-out (0.00s)
        graph_test.go:445: TaskGraph.CalculateConcurrency is not implemented
    --- FAIL: TestTaskGraphCalculateConcurrencyAcrossTopologies/fan-in (0.00s)
        graph_test.go:445: TaskGraph.CalculateConcurrency is not implemented
--- FAIL: TestTaskGraphIsCompleteReturnsTrueWhenAllTasksFinished (0.00s)
    graph_test.go:462: TaskGraph.IsComplete is not implemented
FAIL
FAIL	github.com/anomalyco/yolo-runner/internal/scheduler	0.517s
FAIL
```
