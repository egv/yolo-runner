package scheduler

import (
	"fmt"
	"sort"
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

	return graph, nil
}

func (g TaskGraph) DependenciesOf(taskID string) []string {
	return append([]string(nil), g.dependencies[taskID]...)
}

func (g TaskGraph) DependentsOf(taskID string) []string {
	return append([]string(nil), g.dependents[taskID]...)
}

func (g TaskGraph) ReadySet() []string {
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

	ready := g.ReadySet()
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
	node, exists := g.nodes[taskID]
	if !exists {
		return fmt.Errorf("task %q not found", taskID)
	}
	node.State = state
	g.nodes[taskID] = node
	return nil
}

func (g TaskGraph) InspectNode(taskID string) (NodeInspection, error) {
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
		DependsOn:  g.DependenciesOf(taskID),
		Dependents: g.DependentsOf(taskID),
	}, nil
}
