---
id: yr-fcvl
status: open
deps: [yr-ho6u, yr-1o1m]
links: []
created: 2026-02-21T20:24:45Z
type: task
priority: -1
assignee: Gennady Evstratov
---
# Implement TaskEngine concurrency calculator

Implement CalculateConcurrency method to determine optimal parallel execution count from graph structure.

## Description
Create smart concurrency calculation that analyzes the dependency graph to find maximum parallelizable tasks while respecting resource constraints.

## Acceptance Criteria
- Given failing tests from Task 4, when implementation complete, CalculateConcurrency tests pass
- Given linear task chain (A→B→C), when calculated, returns 1 (no parallelism)
- Given diamond pattern (A→B,C→D), when calculated, returns 2 (B and C parallel)
- Given fan-out (A→B,C,D,E), when calculated, returns 4 (all parallel)
- Given complex graph, when calculated, respects MaxWorkers limit from options
- Given CPU count 8, when calculated, result does not exceed 16 (2x CPU rule)
- Given empty graph, when calculated, returns 0

## TDD Protocol
- Ensure tests are failing before implementation
- Implement depth-level analysis algorithm
- Add resource constraint logic
- Refactor while keeping tests green

## Dependencies
- Depends on: yr-ho6u
- Depends on: 

## Links
- Epic: yr-abz7

