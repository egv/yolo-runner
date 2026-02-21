package engine

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

func TestTaskEngineBuildGraphParentChildRelations(t *testing.T) {
	engine := NewTaskEngine()
	tree := &contracts.TaskTree{
		Root: contracts.Task{ID: "root", Title: "Root", Status: contracts.TaskStatusOpen},
		Tasks: map[string]contracts.Task{
			"root": {ID: "root", Title: "Root", Status: contracts.TaskStatusOpen},
			"a":    {ID: "a", Title: "A", Status: contracts.TaskStatusOpen},
			"b":    {ID: "b", Title: "B", Status: contracts.TaskStatusOpen},
		},
		Relations: []contracts.TaskRelation{
			{FromID: "root", ToID: "a", Type: contracts.RelationParent},
			{FromID: "a", ToID: "b", Type: contracts.RelationParent},
		},
	}

	graph, err := engine.BuildGraph(tree)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	if graph.RootID != "root" {
		t.Fatalf("RootID = %q, want %q", graph.RootID, "root")
	}
	if len(graph.Nodes) != 3 {
		t.Fatalf("len(graph.Nodes) = %d, want %d", len(graph.Nodes), 3)
	}

	root := graph.Nodes["root"]
	a := graph.Nodes["a"]
	b := graph.Nodes["b"]
	if root == nil || a == nil || b == nil {
		t.Fatalf("expected nodes root/a/b to exist")
	}

	if a.Parent == nil || a.Parent.ID != "root" {
		t.Fatalf("a.Parent = %#v, want root", a.Parent)
	}
	if b.Parent == nil || b.Parent.ID != "a" {
		t.Fatalf("b.Parent = %#v, want a", b.Parent)
	}
	if got := childIDs(root.Children); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("root.Children = %v, want [a]", got)
	}
	if got := childIDs(a.Children); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("a.Children = %v, want [b]", got)
	}
	if got := b.Depth; got != 2 {
		t.Fatalf("b.Depth = %d, want 2", got)
	}
}

func TestTaskEngineBuildGraphDependencyAndDependentRelations(t *testing.T) {
	engine := NewTaskEngine()
	tree := &contracts.TaskTree{
		Root: contracts.Task{ID: "root", Status: contracts.TaskStatusOpen},
		Tasks: map[string]contracts.Task{
			"root": {ID: "root", Status: contracts.TaskStatusOpen},
			"a":    {ID: "a", Status: contracts.TaskStatusOpen},
			"b":    {ID: "b", Status: contracts.TaskStatusOpen},
			"c":    {ID: "c", Status: contracts.TaskStatusOpen},
		},
		Relations: []contracts.TaskRelation{
			{FromID: "b", ToID: "a", Type: contracts.RelationDependsOn},
			{FromID: "a", ToID: "c", Type: contracts.RelationBlocks},
		},
	}

	graph, err := engine.BuildGraph(tree)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	a := graph.Nodes["a"]
	b := graph.Nodes["b"]
	c := graph.Nodes["c"]
	if got := depIDs(b.Dependencies); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("b.Dependencies = %v, want [a]", got)
	}
	if got := depIDs(c.Dependencies); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("c.Dependencies = %v, want [a]", got)
	}
	if got := depIDs(a.Dependents); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("a.Dependents = %v, want [b c]", got)
	}
}

func TestTaskEngineBuildGraphMixedRelations(t *testing.T) {
	engine := NewTaskEngine()
	tree := &contracts.TaskTree{
		Root: contracts.Task{ID: "root", Status: contracts.TaskStatusOpen},
		Tasks: map[string]contracts.Task{
			"root": {ID: "root", Status: contracts.TaskStatusOpen},
			"a":    {ID: "a", Status: contracts.TaskStatusOpen},
			"b":    {ID: "b", Status: contracts.TaskStatusOpen},
			"c":    {ID: "c", Status: contracts.TaskStatusOpen},
		},
		Relations: []contracts.TaskRelation{
			{FromID: "root", ToID: "a", Type: contracts.RelationParent},
			{FromID: "root", ToID: "b", Type: contracts.RelationParent},
			{FromID: "b", ToID: "a", Type: contracts.RelationDependsOn},
			{FromID: "a", ToID: "c", Type: contracts.RelationParent},
			{FromID: "c", ToID: "b", Type: contracts.RelationBlocks},
		},
	}

	graph, err := engine.BuildGraph(tree)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	root := graph.Nodes["root"]
	a := graph.Nodes["a"]
	b := graph.Nodes["b"]
	c := graph.Nodes["c"]

	if got := childIDs(root.Children); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("root.Children = %v, want [a b]", got)
	}
	if c.Parent == nil || c.Parent.ID != "a" {
		t.Fatalf("c.Parent = %#v, want a", c.Parent)
	}
	if got := depIDs(b.Dependencies); !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Fatalf("b.Dependencies = %v, want [a c]", got)
	}
	if got := depIDs(a.Dependents); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("a.Dependents = %v, want [b]", got)
	}
	if got := depIDs(c.Dependents); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("c.Dependents = %v, want [b]", got)
	}
}

func TestTaskEngineBuildGraphRejectsCircularDependenciesWithPath(t *testing.T) {
	engine := NewTaskEngine()
	tree := &contracts.TaskTree{
		Root: contracts.Task{ID: "a", Status: contracts.TaskStatusOpen},
		Tasks: map[string]contracts.Task{
			"a": {ID: "a", Status: contracts.TaskStatusOpen},
			"b": {ID: "b", Status: contracts.TaskStatusOpen},
			"c": {ID: "c", Status: contracts.TaskStatusOpen},
		},
		Relations: []contracts.TaskRelation{
			{FromID: "a", ToID: "b", Type: contracts.RelationDependsOn},
			{FromID: "b", ToID: "c", Type: contracts.RelationDependsOn},
			{FromID: "c", ToID: "a", Type: contracts.RelationDependsOn},
		},
	}

	_, err := engine.BuildGraph(tree)
	if err == nil {
		t.Fatalf("expected circular dependency error, got nil")
	}
	if !strings.Contains(err.Error(), "circular dependency") {
		t.Fatalf("expected circular dependency message, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "a -> b -> c -> a") {
		t.Fatalf("expected cycle path in error, got %q", err.Error())
	}
}

func TestTaskEngineBuildGraphPerformance1000TasksUnder100ms(t *testing.T) {
	engine := NewTaskEngine()
	const totalTasks = 1000

	tasks := make(map[string]contracts.Task, totalTasks)
	relations := make([]contracts.TaskRelation, 0, (totalTasks-1)*2)

	tasks["root"] = contracts.Task{ID: "root", Status: contracts.TaskStatusOpen}
	for i := 1; i < totalTasks; i++ {
		id := fmt.Sprintf("t-%04d", i)
		tasks[id] = contracts.Task{ID: id, Status: contracts.TaskStatusOpen}
		relations = append(relations, contracts.TaskRelation{FromID: "root", ToID: id, Type: contracts.RelationParent})
		if i > 1 {
			prev := fmt.Sprintf("t-%04d", i-1)
			relations = append(relations, contracts.TaskRelation{FromID: id, ToID: prev, Type: contracts.RelationDependsOn})
		}
	}

	tree := &contracts.TaskTree{
		Root:      tasks["root"],
		Tasks:     tasks,
		Relations: relations,
	}

	started := time.Now()
	graph, err := engine.BuildGraph(tree)
	duration := time.Since(started)

	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}
	if graph == nil {
		t.Fatalf("BuildGraph() returned nil graph")
	}
	if duration >= 100*time.Millisecond {
		t.Fatalf("BuildGraph() duration = %s, want <100ms", duration)
	}
}

func childIDs(nodes []*contracts.TaskNode) []string {
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}
	return ids
}

func depIDs(nodes []*contracts.TaskNode) []string {
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}
	return ids
}
