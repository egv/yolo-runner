package contracts

import (
	"reflect"
	"testing"
)

func TestTaskEngineInterfaceIncludesRequiredMethods(t *testing.T) {
	engineType := reflect.TypeOf((*TaskEngine)(nil)).Elem()

	required := map[string]reflect.Type{
		"BuildGraph":           reflect.TypeOf(func(*TaskTree) (*TaskGraph, error) { return nil, nil }),
		"GetNextAvailable":     reflect.TypeOf(func(*TaskGraph) []TaskSummary { return nil }),
		"CalculateConcurrency": reflect.TypeOf(func(*TaskGraph, ConcurrencyOptions) int { return 0 }),
		"UpdateTaskStatus":     reflect.TypeOf(func(*TaskGraph, string, TaskStatus) error { return nil }),
		"IsComplete":           reflect.TypeOf(func(*TaskGraph) bool { return false }),
	}

	for methodName, methodType := range required {
		method, ok := engineType.MethodByName(methodName)
		if !ok {
			t.Fatalf("expected TaskEngine to include method %q", methodName)
		}
		if method.Type != methodType {
			t.Fatalf("method %s has signature %s, want %s", methodName, method.Type, methodType)
		}
	}
}

func TestTaskGraphContainsNodesAndEdges(t *testing.T) {
	root := &TaskNode{ID: "root"}
	child := &TaskNode{ID: "child"}
	graph := TaskGraph{
		RootID: "root",
		Nodes: map[string]*TaskNode{
			"root":  root,
			"child": child,
		},
		Edges: []TaskEdge{
			{FromID: "root", ToID: "child", Type: RelationParent},
		},
	}

	if len(graph.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(graph.Nodes))
	}
	if len(graph.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(graph.Edges))
	}
	if graph.Edges[0].FromID != "root" || graph.Edges[0].ToID != "child" {
		t.Fatalf("expected directed edge root->child, got %#v", graph.Edges[0])
	}
}

func TestTaskNodeContainsRequiredSchedulingFields(t *testing.T) {
	parent := &TaskNode{ID: "parent"}
	child := &TaskNode{ID: "child"}
	dependency := &TaskNode{ID: "dependency"}
	dependent := &TaskNode{ID: "dependent"}

	node := TaskNode{
		ID:           "child",
		Task:         Task{ID: "child", Title: "Child Task"},
		Status:       TaskStatusOpen,
		Parent:       parent,
		Children:     []*TaskNode{child},
		Dependencies: []*TaskNode{dependency},
		Dependents:   []*TaskNode{dependent},
		Depth:        2,
		Priority:     10,
	}

	if node.ID != "child" {
		t.Fatalf("expected ID %q, got %q", "child", node.ID)
	}
	if node.Task.ID != "child" {
		t.Fatalf("expected embedded Task ID %q, got %q", "child", node.Task.ID)
	}
	if node.Status != TaskStatusOpen {
		t.Fatalf("expected status %q, got %q", TaskStatusOpen, node.Status)
	}
	if node.Parent == nil || node.Parent.ID != "parent" {
		t.Fatalf("expected parent with ID %q, got %#v", "parent", node.Parent)
	}
	if len(node.Children) != 1 || node.Children[0].ID != "child" {
		t.Fatalf("expected child list to include ID %q, got %#v", "child", node.Children)
	}
	if len(node.Dependencies) != 1 || node.Dependencies[0].ID != "dependency" {
		t.Fatalf("expected dependency list to include ID %q, got %#v", "dependency", node.Dependencies)
	}
	if len(node.Dependents) != 1 || node.Dependents[0].ID != "dependent" {
		t.Fatalf("expected dependent list to include ID %q, got %#v", "dependent", node.Dependents)
	}
	if node.Depth != 2 {
		t.Fatalf("expected depth %d, got %d", 2, node.Depth)
	}
	if node.Priority != 10 {
		t.Fatalf("expected priority %d, got %d", 10, node.Priority)
	}
}

func TestConcurrencyOptionsContainsResourceFields(t *testing.T) {
	opts := ConcurrencyOptions{
		MaxWorkers:     8,
		CPUCount:       16,
		MemoryGB:       32,
		TaskComplexity: 4,
	}

	if opts.MaxWorkers != 8 {
		t.Fatalf("expected MaxWorkers %d, got %d", 8, opts.MaxWorkers)
	}
	if opts.CPUCount != 16 {
		t.Fatalf("expected CPUCount %d, got %d", 16, opts.CPUCount)
	}
	if opts.MemoryGB != 32 {
		t.Fatalf("expected MemoryGB %d, got %d", 32, opts.MemoryGB)
	}
	if opts.TaskComplexity != 4 {
		t.Fatalf("expected TaskComplexity %d, got %d", 4, opts.TaskComplexity)
	}
}

func TestTaskEngineContractCanBeImplementedByFakes(t *testing.T) {
	engine := fakeTaskEngine{}
	graph, err := engine.BuildGraph(&TaskTree{
		Root:  Task{ID: "root"},
		Tasks: map[string]Task{"root": {ID: "root"}},
	})
	if err != nil {
		t.Fatalf("BuildGraph returned error: %v", err)
	}

	available := engine.GetNextAvailable(graph)
	if len(available) != 1 || available[0].ID != "root" {
		t.Fatalf("expected root task to be available, got %#v", available)
	}

	concurrency := engine.CalculateConcurrency(graph, ConcurrencyOptions{MaxWorkers: 3})
	if concurrency != 1 {
		t.Fatalf("expected fixed fake concurrency 1, got %d", concurrency)
	}

	if err := engine.UpdateTaskStatus(graph, "root", TaskStatusClosed); err != nil {
		t.Fatalf("UpdateTaskStatus returned error: %v", err)
	}
	if !engine.IsComplete(graph) {
		t.Fatalf("expected graph to be complete after closing root task")
	}
}

var _ TaskEngine = fakeTaskEngine{}

type fakeTaskEngine struct{}

func (fakeTaskEngine) BuildGraph(tree *TaskTree) (*TaskGraph, error) {
	rootNode := &TaskNode{ID: tree.Root.ID, Task: tree.Root, Status: tree.Root.Status}
	graph := &TaskGraph{
		RootID: tree.Root.ID,
		Nodes: map[string]*TaskNode{
			tree.Root.ID: rootNode,
		},
	}
	return graph, nil
}

func (fakeTaskEngine) GetNextAvailable(graph *TaskGraph) []TaskSummary {
	if graph == nil || graph.Nodes == nil {
		return nil
	}
	root := graph.Nodes[graph.RootID]
	if root == nil {
		return nil
	}
	return []TaskSummary{{ID: root.ID, Title: root.Task.Title}}
}

func (fakeTaskEngine) CalculateConcurrency(_ *TaskGraph, _ ConcurrencyOptions) int {
	return 1
}

func (fakeTaskEngine) UpdateTaskStatus(graph *TaskGraph, taskID string, status TaskStatus) error {
	if graph == nil || graph.Nodes == nil {
		return nil
	}
	node := graph.Nodes[taskID]
	if node == nil {
		return nil
	}
	node.Status = status
	return nil
}

func (fakeTaskEngine) IsComplete(graph *TaskGraph) bool {
	if graph == nil || graph.Nodes == nil {
		return true
	}
	for _, node := range graph.Nodes {
		if node.Status != TaskStatusClosed && node.Status != TaskStatusFailed && node.Status != TaskStatusBlocked {
			return false
		}
	}
	return true
}
