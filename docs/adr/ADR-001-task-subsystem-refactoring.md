# ADR-001: Task Subsystem Architecture Refactoring

## Status
Accepted

## Context

The current task management system in yolo-agent has architectural limitations that prevent proper handling of task hierarchies and relationships. The `TaskManager` interface conflates storage concerns with scheduling logic, leading to:

1. **Incorrect hierarchy handling**: GitHub issues are fetched as a flat list, treating all repository issues as children of the specified root, regardless of actual parent-child relationships
2. **No dependency resolution**: Tasks cannot express "depends-on" relationships that affect execution order
3. **Inflexible concurrency**: Concurrency is a static number rather than calculated from the dependency graph structure
4. **Tight coupling**: Storage backend details leak into scheduling decisions

When attempting to run yolo-agent on GitHub Epic #52 (Distribution & Installation), the system incorrectly processes Epic #53 and other unrelated issues as children of #52 because the GitHub tracker fetches ALL repository issues and assigns them to the root.

## Decision

We will refactor the task subsystem into two distinct layers:

### 1. Storage Backend Layer
A pluggable abstraction responsible solely for data persistence operations:
- Authentication with external services (GitHub, Linear, etc.)
- Fetching task hierarchies and relationships
- Persisting status updates and metadata

**Interface:**
```go
type StorageBackend interface {
    GetTaskTree(ctx context.Context, rootID string) (*TaskTree, error)
    GetTask(ctx context.Context, taskID string) (*Task, error)
    SetTaskStatus(ctx context.Context, taskID string, status TaskStatus) error
    SetTaskData(ctx context.Context, taskID string, data map[string]string) error
}
```

### 2. Task Engine Layer
A graph-based scheduler responsible for:
- Building directed graphs from task relationships
- Resolving dependencies to determine execution order
- Calculating optimal concurrency based on graph structure
- Managing task state transitions

**Interface:**
```go
type TaskEngine interface {
    BuildGraph(tree *TaskTree) (*TaskGraph, error)
    GetNextAvailable(graph *TaskGraph) []TaskSummary
    CalculateConcurrency(graph *TaskGraph, opts ConcurrencyOptions) int
    UpdateTaskStatus(graph *TaskGraph, taskID string, status TaskStatus)
    IsComplete(graph *TaskGraph) bool
}
```

## Consequences

### Positive
- **Proper hierarchy support**: GitHub sub-issues, Linear relationships handled correctly
- **Dependency-aware scheduling**: Tasks can declare blockers and dependencies
- **Smart concurrency**: Automatically calculates parallelism from dependency graph
- **Testability**: Clear separation allows mocking storage for engine tests
- **Extensibility**: New storage backends (Jira, Asana, etc.) only need to implement interface
- **Resilience**: Periodic re-fetching of task tree picks up external changes

### Negative
- **Increased complexity**: Two layers instead of one
- **Migration effort**: Need to refactor existing GitHub/Linear/tk implementations
- **Performance**: Building graph on each iteration adds overhead (mitigated by caching)

### Risks
- **Breaking changes**: Existing task management code needs updates
- **Learning curve**: Developers must understand two interfaces instead of one

## Implementation Plan

1. **Phase 1**: Define interfaces in `internal/contracts`
2. **Phase 2**: Implement `GitHubStorageBackend` with proper sub-issue support
3. **Phase 3**: Implement `TaskEngine` with graph builder and concurrency calculator
4. **Phase 4**: Refactor agent loop to use new architecture
5. **Phase 5**: Migrate Linear and tk backends
6. **Phase 6**: Deprecate old `TaskManager` interface

## Dogfooding Strategy

This work will be implemented using the current yolo-agent system to validate:
1. The existing system can handle complex refactoring tasks
2. The new architecture solves real problems encountered during implementation
3. The migration path is practical and well-tested

## Alternatives Considered

### Option A: Fix GitHub tracker only
Modify the existing GitHub TaskManager to filter by `parent_issue_id`.

**Rejected**: Doesn't solve the architectural problem; other backends would still have issues.

### Option B: Add dependency field to Task struct
Extend the current Task struct with dependency IDs and handle in TaskManager.

**Rejected**: Still mixes storage and scheduling concerns; doesn't address hierarchy issues.

### Option C: Use external graph database
Store task relationships in Neo4j or similar.

**Rejected**: Overkill for current needs; adds operational complexity.

## Related

- Epic: Task Subsystem Refactoring
- Design Doc: `docs/design/task-subsystem-refactor.md`
- Affected: `internal/github`, `internal/linear`, `internal/tk`, `internal/agent`

## Decision Date
2026-02-21

## Decision Makers
- egv (architect)

## Notes

This refactoring enables the core feature of intelligent task execution - analyzing dependency graphs to calculate optimal concurrency. Without this separation, the system cannot properly handle complex task hierarchies.

Priority: P(-1) - Critical architectural improvement
