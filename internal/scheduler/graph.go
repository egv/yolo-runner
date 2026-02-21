package scheduler

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

type TaskState string

const (
	TaskStatePending   TaskState = "pending"
	TaskStateRunning   TaskState = "running"
	TaskStateSucceeded TaskState = "succeeded"
	TaskStateFailed    TaskState = "failed"
	TaskStateCanceled  TaskState = "canceled"
)

func (s TaskState) IsTerminal() bool {
	switch s {
	case TaskStateSucceeded, TaskStateFailed, TaskStateCanceled:
		return true
	default:
		return false
	}
}

type TaskNode struct {
	ID        string
	State     TaskState
	DependsOn []string
}

type TaskGraph struct {
	mu           sync.RWMutex
	nodes        map[string]TaskNode
	dependencies map[string][]string
	dependents   map[string][]string
}

type SchedulerContract interface {
	ReadySet() []string
	InspectNode(taskID string) (NodeInspection, error)
}

type NodeInspection struct {
	TaskID     string
	State      TaskState
	Ready      bool
	Terminal   bool
	DependsOn  []string
	Dependents []string
}

func NewTaskGraph(nodes []TaskNode) (TaskGraph, error) {
	graph := TaskGraph{
		nodes:        make(map[string]TaskNode, len(nodes)),
		dependencies: make(map[string][]string, len(nodes)),
		dependents:   make(map[string][]string, len(nodes)),
	}

	for _, node := range nodes {
		if node.ID == "" {
			return TaskGraph{}, fmt.Errorf("task id cannot be empty")
		}
		if _, exists := graph.nodes[node.ID]; exists {
			return TaskGraph{}, fmt.Errorf("duplicate task id %q", node.ID)
		}

		graph.nodes[node.ID] = node
		deps := append([]string(nil), node.DependsOn...)
		sort.Strings(deps)
		graph.dependencies[node.ID] = deps
	}

	for id, deps := range graph.dependencies {
		for _, depID := range deps {
			if _, exists := graph.nodes[depID]; !exists {
				return TaskGraph{}, fmt.Errorf("task %q depends on unknown task %q", id, depID)
			}
			graph.dependents[depID] = append(graph.dependents[depID], id)
		}
	}

	for id, dependents := range graph.dependents {
		sort.Strings(dependents)
		graph.dependents[id] = dependents
	}
	if cycle := findDependencyCycle(graph.dependencies); len(cycle) > 0 {
		return TaskGraph{}, fmt.Errorf("circular dependency detected: %s", strings.Join(cycle, " -> "))
	}

	return graph, nil
}

func (g TaskGraph) DependenciesOf(taskID string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return append([]string(nil), g.dependencies[taskID]...)
}

func (g TaskGraph) DependentsOf(taskID string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return append([]string(nil), g.dependents[taskID]...)
}

func (g TaskGraph) ReadySet() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.readySetLocked()
}

func (g TaskGraph) readySetLocked() []string {
	ready := make([]string, 0)

	for id, node := range g.nodes {
		if node.State != TaskStatePending {
			continue
		}

		depsSatisfied := true
		for _, depID := range g.dependencies[id] {
			dep := g.nodes[depID]
			if dep.State != TaskStateSucceeded {
				depsSatisfied = false
				break
			}
		}

		if depsSatisfied {
			ready = append(ready, id)
		}
	}

	sort.Strings(ready)
	return ready
}

func (g *TaskGraph) ReserveReady(limit int) []string {
	if limit <= 0 {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	ready := g.readySetLocked()
	if len(ready) > limit {
		ready = ready[:limit]
	}

	for _, taskID := range ready {
		node := g.nodes[taskID]
		node.State = TaskStateRunning
		g.nodes[taskID] = node
	}

	return ready
}

func (g *TaskGraph) SetState(taskID string, state TaskState) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	node, exists := g.nodes[taskID]
	if !exists {
		return fmt.Errorf("task %q not found", taskID)
	}
	node.State = state
	g.nodes[taskID] = node
	return nil
}

func (g TaskGraph) InspectNode(taskID string) (NodeInspection, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	node, exists := g.nodes[taskID]
	if !exists {
		return NodeInspection{}, fmt.Errorf("task %q not found", taskID)
	}

	ready := false
	if node.State == TaskStatePending {
		ready = true
		for _, depID := range g.dependencies[taskID] {
			dep := g.nodes[depID]
			if dep.State != TaskStateSucceeded {
				ready = false
				break
			}
		}
	}

	return NodeInspection{
		TaskID:     taskID,
		State:      node.State,
		Ready:      ready,
		Terminal:   node.State.IsTerminal(),
		DependsOn:  append([]string(nil), g.dependencies[taskID]...),
		Dependents: append([]string(nil), g.dependents[taskID]...),
	}, nil
}

func (g *TaskGraph) CalculateConcurrency() int {
	if g == nil {
		return 0
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	if len(g.nodes) == 0 {
		return 0
	}

	inDegree := make(map[string]int, len(g.nodes))
	currentLevel := make([]string, 0, len(g.nodes))
	for taskID := range g.nodes {
		inDegree[taskID] = len(g.dependencies[taskID])
		if inDegree[taskID] == 0 {
			currentLevel = append(currentLevel, taskID)
		}
	}
	sort.Strings(currentLevel)

	maxParallel := 0
	for len(currentLevel) > 0 {
		if len(currentLevel) > maxParallel {
			maxParallel = len(currentLevel)
		}

		nextLevel := make([]string, 0)
		for _, taskID := range currentLevel {
			for _, dependentID := range g.dependents[taskID] {
				inDegree[dependentID]--
				if inDegree[dependentID] == 0 {
					nextLevel = append(nextLevel, dependentID)
				}
			}
		}
		sort.Strings(nextLevel)
		currentLevel = nextLevel
	}

	return maxParallel
}

func (g *TaskGraph) IsComplete() bool {
	if g == nil {
		return true
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	for _, node := range g.nodes {
		if !node.State.IsTerminal() {
			return false
		}
	}
	return true
}

func findDependencyCycle(dependencies map[string][]string) []string {
	const (
		unvisited = iota
		visiting
		visited
	)

	state := make(map[string]int, len(dependencies))
	stack := make([]string, 0, len(dependencies))
	stackIndex := make(map[string]int, len(dependencies))
	orderedIDs := make([]string, 0, len(dependencies))
	for taskID := range dependencies {
		orderedIDs = append(orderedIDs, taskID)
	}
	sort.Strings(orderedIDs)

	var cycle []string
	var dfs func(taskID string) bool
	dfs = func(taskID string) bool {
		state[taskID] = visiting
		stackIndex[taskID] = len(stack)
		stack = append(stack, taskID)

		for _, depID := range dependencies[taskID] {
			switch state[depID] {
			case unvisited:
				if dfs(depID) {
					return true
				}
			case visiting:
				start := stackIndex[depID]
				cycle = append([]string(nil), stack[start:]...)
				cycle = append(cycle, depID)
				return true
			}
		}

		stack = stack[:len(stack)-1]
		delete(stackIndex, taskID)
		state[taskID] = visited
		return false
	}

	for _, taskID := range orderedIDs {
		if state[taskID] != unvisited {
			continue
		}
		if dfs(taskID) {
			return cycle
		}
	}

	return nil
}
