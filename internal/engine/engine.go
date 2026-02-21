package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

// TaskEngine builds and evaluates in-memory task graphs over contracts types.
type TaskEngine struct{}

func NewTaskEngine() *TaskEngine {
	return &TaskEngine{}
}

var _ contracts.TaskEngine = (*TaskEngine)(nil)

func (e *TaskEngine) BuildGraph(tree *contracts.TaskTree) (*contracts.TaskGraph, error) {
	if tree == nil {
		return nil, fmt.Errorf("task tree is required")
	}

	rootID := strings.TrimSpace(tree.Root.ID)
	if rootID == "" {
		return nil, fmt.Errorf("task tree root ID is required")
	}

	tasks, err := normalizeTasks(tree, rootID)
	if err != nil {
		return nil, err
	}

	nodes := make(map[string]*contracts.TaskNode, len(tasks))
	taskIDs := sortedTaskIDs(tasks)
	for _, taskID := range taskIDs {
		task := tasks[taskID]
		nodes[taskID] = &contracts.TaskNode{
			ID:       taskID,
			Task:     task,
			Status:   task.Status,
			Priority: taskPriority(task),
		}
	}

	edges := make([]contracts.TaskEdge, 0, len(tree.Relations))
	edgeSeen := make(map[string]struct{}, len(tree.Relations))
	parentSeen := make(map[string]struct{}, len(tree.Relations))
	dependencySeen := make(map[string]struct{}, len(tree.Relations))
	dependencies := make(map[string][]string, len(nodes))
	for taskID := range nodes {
		dependencies[taskID] = nil
	}

	for _, relation := range tree.Relations {
		fromID := strings.TrimSpace(relation.FromID)
		toID := strings.TrimSpace(relation.ToID)
		if fromID == "" || toID == "" {
			return nil, fmt.Errorf("relation endpoints cannot be empty for type %q", relation.Type)
		}
		if fromID == toID {
			if relation.Type == contracts.RelationDependsOn || relation.Type == contracts.RelationBlocks {
				return nil, fmt.Errorf("circular dependency detected: %s -> %s", fromID, toID)
			}
			return nil, fmt.Errorf("self-referential relation is not allowed: %s -> %s (%s)", fromID, toID, relation.Type)
		}

		fromNode := nodes[fromID]
		toNode := nodes[toID]
		if fromNode == nil {
			return nil, fmt.Errorf("relation %q references unknown task %q", relation.Type, fromID)
		}
		if toNode == nil {
			return nil, fmt.Errorf("relation %q references unknown task %q", relation.Type, toID)
		}

		edgeKey := edgeKey(relation.Type, fromID, toID)
		if _, exists := edgeSeen[edgeKey]; !exists {
			edges = append(edges, contracts.TaskEdge{FromID: fromID, ToID: toID, Type: relation.Type})
			edgeSeen[edgeKey] = struct{}{}
		}

		switch relation.Type {
		case contracts.RelationParent:
			if err := linkParent(parentSeen, fromNode, toNode); err != nil {
				return nil, err
			}
		case contracts.RelationDependsOn:
			linkDependency(dependencies, dependencySeen, fromNode, toNode)
		case contracts.RelationBlocks:
			linkDependency(dependencies, dependencySeen, toNode, fromNode)
		default:
			return nil, fmt.Errorf("unsupported relation type %q", relation.Type)
		}
	}

	if cycle := findDependencyCycle(dependencies); len(cycle) > 0 {
		return nil, fmt.Errorf("circular dependency detected: %s", strings.Join(cycle, " -> "))
	}

	if err := assignDepths(nodes, rootID); err != nil {
		return nil, err
	}
	sortNodeLinks(nodes)
	sortTaskEdges(edges)

	return &contracts.TaskGraph{
		RootID: rootID,
		Nodes:  nodes,
		Edges:  edges,
	}, nil
}

func (e *TaskEngine) GetNextAvailable(graph *contracts.TaskGraph) []contracts.TaskSummary {
	if graph == nil || len(graph.Nodes) == 0 {
		return nil
	}

	ids := make([]string, 0, len(graph.Nodes))
	for id := range graph.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	available := make([]contracts.TaskSummary, 0, len(ids))
	for _, id := range ids {
		node := graph.Nodes[id]
		if node == nil || node.Status != contracts.TaskStatusOpen {
			continue
		}
		if dependenciesSatisfied(node) {
			available = append(available, contracts.TaskSummary{
				ID:       node.ID,
				Title:    node.Task.Title,
				Priority: intPointer(node.Priority),
			})
		}
	}
	return available
}

func (e *TaskEngine) CalculateConcurrency(graph *contracts.TaskGraph, opts contracts.ConcurrencyOptions) int {
	if graph == nil || len(graph.Nodes) == 0 {
		return 0
	}

	depthCount := make(map[int]int, len(graph.Nodes))
	for _, node := range graph.Nodes {
		depthCount[node.Depth]++
	}

	maxParallel := 0
	for _, count := range depthCount {
		if count > maxParallel {
			maxParallel = count
		}
	}

	limit := maxParallel
	if opts.MaxWorkers > 0 && limit > opts.MaxWorkers {
		limit = opts.MaxWorkers
	}
	if opts.CPUCount > 0 {
		cpuLimit := opts.CPUCount * 2
		if limit > cpuLimit {
			limit = cpuLimit
		}
	}
	if limit < 1 {
		return 1
	}
	return limit
}

func (e *TaskEngine) UpdateTaskStatus(graph *contracts.TaskGraph, taskID string, status contracts.TaskStatus) {
	if graph == nil || len(graph.Nodes) == 0 {
		return
	}

	node := graph.Nodes[taskID]
	if node == nil {
		return
	}
	node.Status = status
	node.Task.Status = status
}

func (e *TaskEngine) IsComplete(graph *contracts.TaskGraph) bool {
	if graph == nil || len(graph.Nodes) == 0 {
		return true
	}

	for _, node := range graph.Nodes {
		if !isFinishedStatus(node.Status) {
			return false
		}
	}
	return true
}

func normalizeTasks(tree *contracts.TaskTree, rootID string) (map[string]contracts.Task, error) {
	normalized := make(map[string]contracts.Task, len(tree.Tasks)+1)
	for mapKey, task := range tree.Tasks {
		taskID := strings.TrimSpace(task.ID)
		if taskID == "" {
			taskID = strings.TrimSpace(mapKey)
		}
		if taskID == "" {
			return nil, fmt.Errorf("task ID cannot be empty")
		}

		if existing, exists := normalized[taskID]; exists && !sameTask(existing, task) {
			return nil, fmt.Errorf("duplicate task ID %q", taskID)
		}
		task.ID = taskID
		normalized[taskID] = task
	}

	rootTask := tree.Root
	rootTask.ID = rootID
	if existing, exists := normalized[rootID]; !exists {
		normalized[rootID] = rootTask
	} else {
		if rootTask.Title != "" {
			existing.Title = rootTask.Title
		}
		if rootTask.Description != "" {
			existing.Description = rootTask.Description
		}
		if rootTask.Status != "" {
			existing.Status = rootTask.Status
		}
		if rootTask.ParentID != "" {
			existing.ParentID = rootTask.ParentID
		}
		if len(rootTask.Metadata) > 0 {
			existing.Metadata = cloneMetadata(rootTask.Metadata)
		}
		normalized[rootID] = existing
	}

	return normalized, nil
}

func sortedTaskIDs(tasks map[string]contracts.Task) []string {
	ids := make([]string, 0, len(tasks))
	for id := range tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func taskPriority(task contracts.Task) int {
	if task.Metadata == nil {
		return 0
	}
	priority, ok := task.Metadata["priority"]
	if !ok {
		return 0
	}
	priority = strings.TrimSpace(priority)
	if priority == "" {
		return 0
	}

	value := 0
	negative := false
	for i, r := range priority {
		if i == 0 && r == '-' {
			negative = true
			continue
		}
		if r < '0' || r > '9' {
			return 0
		}
		value = value*10 + int(r-'0')
	}
	if negative {
		value = -value
	}
	return value
}

func linkParent(parentSeen map[string]struct{}, parent *contracts.TaskNode, child *contracts.TaskNode) error {
	pairKey := parent.ID + "|" + child.ID
	if _, exists := parentSeen[pairKey]; exists {
		return nil
	}

	if child.Parent != nil && child.Parent.ID != parent.ID {
		return fmt.Errorf("task %q has multiple parents: %q and %q", child.ID, child.Parent.ID, parent.ID)
	}
	child.Parent = parent
	parent.Children = append(parent.Children, child)
	parentSeen[pairKey] = struct{}{}
	return nil
}

func linkDependency(dependencies map[string][]string, dependencySeen map[string]struct{}, task *contracts.TaskNode, dependency *contracts.TaskNode) {
	pairKey := task.ID + "|" + dependency.ID
	if _, exists := dependencySeen[pairKey]; exists {
		return
	}

	task.Dependencies = append(task.Dependencies, dependency)
	dependency.Dependents = append(dependency.Dependents, task)
	dependencies[task.ID] = append(dependencies[task.ID], dependency.ID)
	dependencySeen[pairKey] = struct{}{}
}

func assignDepths(nodes map[string]*contracts.TaskNode, rootID string) error {
	depthByID := make(map[string]int, len(nodes))
	state := make(map[string]int, len(nodes))
	stack := make([]string, 0, len(nodes))
	stackPos := make(map[string]int, len(nodes))

	if root, ok := nodes[rootID]; ok {
		depthByID[rootID] = 0
		root.Depth = 0
	}

	const (
		unvisited = iota
		visiting
		visited
	)

	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var visit func(string) (int, error)
	visit = func(id string) (int, error) {
		switch state[id] {
		case visited:
			return depthByID[id], nil
		case visiting:
			start := stackPos[id]
			cycle := append([]string(nil), stack[start:]...)
			cycle = append(cycle, id)
			return 0, fmt.Errorf("parent cycle detected: %s", strings.Join(cycle, " -> "))
		}

		state[id] = visiting
		stackPos[id] = len(stack)
		stack = append(stack, id)

		node := nodes[id]
		depth := 0
		if node.Parent != nil {
			parentDepth, err := visit(node.Parent.ID)
			if err != nil {
				return 0, err
			}
			depth = parentDepth + 1
		}

		stack = stack[:len(stack)-1]
		delete(stackPos, id)
		state[id] = visited
		depthByID[id] = depth
		node.Depth = depth
		return depth, nil
	}

	for _, id := range ids {
		if _, err := visit(id); err != nil {
			return err
		}
	}

	return nil
}

func sortNodeLinks(nodes map[string]*contracts.TaskNode) {
	for _, node := range nodes {
		sort.Slice(node.Children, func(i int, j int) bool {
			return node.Children[i].ID < node.Children[j].ID
		})
		sort.Slice(node.Dependencies, func(i int, j int) bool {
			return node.Dependencies[i].ID < node.Dependencies[j].ID
		})
		sort.Slice(node.Dependents, func(i int, j int) bool {
			return node.Dependents[i].ID < node.Dependents[j].ID
		})
	}
}

func sortTaskEdges(edges []contracts.TaskEdge) {
	sort.Slice(edges, func(i int, j int) bool {
		if edges[i].Type != edges[j].Type {
			return edges[i].Type < edges[j].Type
		}
		if edges[i].FromID != edges[j].FromID {
			return edges[i].FromID < edges[j].FromID
		}
		return edges[i].ToID < edges[j].ToID
	})
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

	for taskID, deps := range dependencies {
		sort.Strings(deps)
		dependencies[taskID] = deps
	}

	var cycle []string
	var dfs func(string) bool
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

func edgeKey(t contracts.RelationType, fromID, toID string) string {
	return string(t) + "|" + fromID + "|" + toID
}

func sameTask(a, b contracts.Task) bool {
	if a.ID != b.ID || a.Title != b.Title || a.Description != b.Description || a.Status != b.Status || a.ParentID != b.ParentID {
		return false
	}
	if len(a.Metadata) != len(b.Metadata) {
		return false
	}
	for key, value := range a.Metadata {
		if b.Metadata[key] != value {
			return false
		}
	}
	return true
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	copy := make(map[string]string, len(metadata))
	for key, value := range metadata {
		copy[key] = value
	}
	return copy
}

func dependenciesSatisfied(node *contracts.TaskNode) bool {
	for _, dependency := range node.Dependencies {
		if dependency == nil || dependency.Status != contracts.TaskStatusClosed {
			return false
		}
	}
	return true
}

func intPointer(value int) *int {
	v := value
	return &v
}

func isFinishedStatus(status contracts.TaskStatus) bool {
	switch status {
	case contracts.TaskStatusClosed, contracts.TaskStatusFailed, contracts.TaskStatusBlocked:
		return true
	default:
		return false
	}
}
