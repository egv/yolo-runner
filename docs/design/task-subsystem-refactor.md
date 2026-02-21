# Task Subsystem Refactoring Design

## Problem Statement

Current implementation mixes storage concerns with task scheduling logic. The `TaskManager` interface tries to do both, leading to:
- Storage backends fetching all repository issues regardless of parent
- No proper separation between fetching data and building task graphs
- Incorrect handling of GitHub sub-issues (all issues treated as children of root)

## Proposed Architecture

### 1. Storage Backend Layer (Pluggable)

Responsible for: Authentication, data fetching, format conversion, pagination

```go
// StorageBackend is the interface for task storage adapters
type StorageBackend interface {
    // GetTaskTree fetches the root task and all related tasks (children, dependencies)
    // Returns a complete snapshot of the task hierarchy
    GetTaskTree(ctx context.Context, rootID string) (*TaskTree, error)
    
    // GetTask fetches a single task by ID
    GetTask(ctx context.Context, taskID string) (*Task, error)
    
    // SetTaskStatus updates task status in storage
    SetTaskStatus(ctx context.Context, taskID string, status TaskStatus) error
    
    // SetTaskData stores arbitrary key-value data for a task
    SetTaskData(ctx context.Context, taskID string, data map[string]string) error
}

// TaskTree represents a snapshot of tasks from storage
type TaskTree struct {
    Root       Task
    Tasks      map[string]Task      // All tasks indexed by ID
    Relations  []TaskRelation       // Parent-child and dependency links
}

// TaskRelation defines relationships between tasks
type TaskRelation struct {
    FromID     string
    ToID       string
    Type       RelationType  // "parent", "depends_on", "blocks"
}

type RelationType string

const (
    RelationParent    RelationType = "parent"
    RelationDependsOn RelationType = "depends_on"
    RelationBlocks    RelationType = "blocks"
)
```

**Implementations:**
- `GitHubBackend` - uses GitHub Issues API with proper sub-issue support
- `LinearBackend` - uses Linear API
- `TKBackend` - uses local markdown files

### 2. Task Engine Layer

Responsible for: Graph construction, dependency resolution, scheduling, concurrency calculation

```go
// TaskEngine manages the task execution graph
type TaskEngine interface {
    // BuildGraph constructs a directed graph from task tree
    BuildGraph(tree *TaskTree) (*TaskGraph, error)
    
    // GetNextAvailable returns tasks that can be executed now
    // (status=open, all dependencies satisfied)
    GetNextAvailable(graph *TaskGraph) []TaskSummary
    
    // CalculateConcurrency determines optimal parallel execution count
    // Based on: available workers, dependency graph structure, resource limits
    CalculateConcurrency(graph *TaskGraph, opts ConcurrencyOptions) int
    
    // UpdateTaskStatus updates status and recalculates dependent tasks
    UpdateTaskStatus(graph *TaskGraph, taskID string, status TaskStatus)
    
    // IsComplete checks if all tasks are finished (completed/failed/blocked)
    IsComplete(graph *TaskGraph) bool
    
    // GetTaskPath returns the critical path for a task
    GetTaskPath(graph *TaskGraph, taskID string) []string
}

// TaskGraph is the in-memory representation of task relationships
type TaskGraph struct {
    RootID    string
    Nodes     map[string]*TaskNode
    mu        sync.RWMutex
}

type TaskNode struct {
    ID           string
    Task         Task
    Status       TaskStatus
    Parent       *TaskNode
    Children     []*TaskNode
    Dependencies []*TaskNode  // Must complete before this task
    Dependents   []*TaskNode  // Tasks waiting on this one
    Depth        int          // Distance from root
    Priority     int
}
```

### 3. Concurrency Calculation

```go
type ConcurrencyOptions struct {
    MaxWorkers     int   // Hard limit from config
    CPUCount       int   // Available CPUs
    MemoryGB       int   // Available memory
    TaskComplexity int   // Estimated complexity (1-10)
}

// CalculateConcurrency implements smart concurrency:
// 1. Analyze dependency graph to find parallelizable tasks
// 2. Consider resource limits (CPU, memory)
// 3. Respect user-defined max
// 4. Default to "auto" if not specified
func (e *taskEngine) CalculateConcurrency(graph *TaskGraph, opts ConcurrencyOptions) int {
    // Count tasks at each depth level
    depthCount := make(map[int]int)
    for _, node := range graph.Nodes {
        depthCount[node.Depth]++
    }
    
    // Max tasks at any depth = theoretical max parallelism
    maxAtDepth := 0
    for _, count := range depthCount {
        if count > maxAtDepth {
            maxAtDepth = count
        }
    }
    
    // Apply limits
    concurrency := min(maxAtDepth, opts.MaxWorkers)
    concurrency = min(concurrency, opts.CPUCount*2) // Rule of thumb
    
    if concurrency == 0 {
        return 1 // Always at least 1
    }
    return concurrency
}
```

### 4. Workflow

```
1. Agent Loop starts with rootID
2. StorageBackend.GetTaskTree(rootID) → TaskTree
3. TaskEngine.BuildGraph(TaskTree) → TaskGraph
4. TaskEngine.GetNextAvailable(TaskGraph) → []TaskSummary
5. TaskEngine.CalculateConcurrency(TaskGraph) → concurrency
6. Agent spawns workers up to concurrency limit
7. Each worker:
   - Gets task from available queue
   - Executes via Runner
   - Returns result
   - StorageBackend.SetTaskStatus(taskID, status)
8. Loop re-fetches TaskTree (for any external changes)
9. TaskEngine.BuildGraph(new TaskTree) → updated TaskGraph
10. Repeat until TaskEngine.IsComplete()
```

### 5. Benefits

1. **Separation of Concerns**: Storage doesn't know about scheduling
2. **Testability**: Can test engine with mock storage
3. **Pluggable**: Easy to add new storage backends
4. **Proper Relationships**: GitHub sub-issues, Linear relations handled correctly
5. **Smart Concurrency**: Based on actual dependency graph, not just count
6. **Resilience**: Re-fetching tree picks up external changes

### 6. Migration Plan

1. Create new `internal/storage` package with Backend interface
2. Refactor GitHub tracker to implement StorageBackend
3. Create `internal/engine` package with TaskEngine
4. Update agent loop to use new interfaces
5. Deprecate old TaskManager interface
6. Add tests for graph building and concurrency

### 7. Open Questions

1. Should we cache TaskTree or fetch fresh each iteration?
2. How to handle external changes to tasks during execution?
3. Should concurrency be dynamic (adjust based on progress)?
4. Do we need priority queuing within available tasks?

## Files to Create/Modify

**New:**
- `internal/storage/backend.go` - interface definitions
- `internal/storage/github/backend.go` - GitHub implementation
- `internal/engine/graph.go` - TaskGraph and TaskNode
- `internal/engine/engine.go` - TaskEngine implementation

**Modify:**
- `internal/contracts/contracts.go` - new interfaces
- `internal/agent/loop.go` - use new TaskEngine
- `cmd/yolo-agent/main.go` - wire up new components

## Acceptance Criteria

- [ ] GitHub backend correctly fetches only sub-issues of root
- [ ] Task engine builds accurate dependency graph
- [ ] Concurrency calculation respects dependency chains
- [ ] All existing functionality preserved
- [ ] Unit tests for graph building
- [ ] Unit tests for concurrency calculation
- [ ] Integration test with GitHub backend
