package engine

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
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

func TestTaskEngineGetNextAvailableReturnsDependencySatisfiedOpenTasks(t *testing.T) {
	engine := NewTaskEngine()
	tree := &contracts.TaskTree{
		Root: contracts.Task{ID: "root", Status: contracts.TaskStatusClosed},
		Tasks: map[string]contracts.Task{
			"root": {ID: "root", Status: contracts.TaskStatusClosed},
			"a":    {ID: "a", Title: "A", Status: contracts.TaskStatusOpen},
			"b":    {ID: "b", Title: "B", Status: contracts.TaskStatusOpen},
			"c":    {ID: "c", Title: "C", Status: contracts.TaskStatusOpen},
			"d":    {ID: "d", Title: "D", Status: contracts.TaskStatusClosed},
			"e":    {ID: "e", Title: "E", Status: contracts.TaskStatusInProgress},
			"f":    {ID: "f", Title: "F", Status: contracts.TaskStatusClosed},
		},
		Relations: []contracts.TaskRelation{
			{FromID: "b", ToID: "a", Type: contracts.RelationDependsOn},
			{FromID: "c", ToID: "d", Type: contracts.RelationDependsOn},
		},
	}

	graph, err := engine.BuildGraph(tree)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	got := summaryIDs(engine.GetNextAvailable(graph))
	want := []string{"a", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetNextAvailable() = %v, want %v", got, want)
	}
}

func TestTaskEngineGetNextAvailableReflectsDependencyCompletionOnNextCall(t *testing.T) {
	engine := NewTaskEngine()
	tree := &contracts.TaskTree{
		Root: contracts.Task{ID: "root", Status: contracts.TaskStatusClosed},
		Tasks: map[string]contracts.Task{
			"root":    {ID: "root", Status: contracts.TaskStatusClosed},
			"dep":     {ID: "dep", Title: "Dependency", Status: contracts.TaskStatusOpen},
			"blocked": {ID: "blocked", Title: "Blocked", Status: contracts.TaskStatusOpen},
		},
		Relations: []contracts.TaskRelation{
			{FromID: "blocked", ToID: "dep", Type: contracts.RelationDependsOn},
		},
	}

	graph, err := engine.BuildGraph(tree)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	if got := summaryIDs(engine.GetNextAvailable(graph)); !reflect.DeepEqual(got, []string{"dep"}) {
		t.Fatalf("GetNextAvailable() before dependency completion = %v, want [dep]", got)
	}

	if err := engine.UpdateTaskStatus(graph, "dep", contracts.TaskStatusClosed); err != nil {
		t.Fatalf("UpdateTaskStatus() error = %v", err)
	}
	if got := summaryIDs(engine.GetNextAvailable(graph)); !reflect.DeepEqual(got, []string{"blocked"}) {
		t.Fatalf("GetNextAvailable() after dependency completion = %v, want [blocked]", got)
	}
}

func TestTaskEngineGetNextAvailableReturnsEmptySliceForNilOrEmptyGraph(t *testing.T) {
	engine := NewTaskEngine()

	testCases := []struct {
		name  string
		graph *contracts.TaskGraph
	}{
		{name: "nil graph", graph: nil},
		{name: "empty graph", graph: &contracts.TaskGraph{}},
		{name: "graph with empty node map", graph: &contracts.TaskGraph{Nodes: map[string]*contracts.TaskNode{}}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := engine.GetNextAvailable(tc.graph)
			if got == nil {
				t.Fatalf("GetNextAvailable() returned nil, want empty slice")
			}
			if len(got) != 0 {
				t.Fatalf("len(GetNextAvailable()) = %d, want 0", len(got))
			}
		})
	}
}

func TestTaskEngineGetNextAvailableSkipsTasksWithMissingDependencyNodes(t *testing.T) {
	engine := NewTaskEngine()
	graph := &contracts.TaskGraph{
		RootID: "root",
		Nodes: map[string]*contracts.TaskNode{
			"root": {
				ID:     "root",
				Task:   contracts.Task{ID: "root", Title: "Root", Status: contracts.TaskStatusClosed},
				Status: contracts.TaskStatusClosed,
			},
			"task": {
				ID:     "task",
				Task:   contracts.Task{ID: "task", Title: "Task", Status: contracts.TaskStatusOpen},
				Status: contracts.TaskStatusOpen,
				Dependencies: []*contracts.TaskNode{
					{ID: "missing-dep", Status: contracts.TaskStatusClosed},
				},
			},
		},
	}

	got := engine.GetNextAvailable(graph)
	if len(got) != 0 {
		t.Fatalf("GetNextAvailable() = %v, want no runnable tasks when dependency node is missing", summaryIDs(got))
	}
}

func TestTaskEngineUpdateTaskStatusReturnsErrorWhenClosingTaskWithOpenDependencies(t *testing.T) {
	engine := NewTaskEngine()
	tree := &contracts.TaskTree{
		Root: contracts.Task{ID: "root", Status: contracts.TaskStatusClosed},
		Tasks: map[string]contracts.Task{
			"root": {ID: "root", Status: contracts.TaskStatusClosed},
			"dep":  {ID: "dep", Title: "Dependency", Status: contracts.TaskStatusOpen},
			"task": {ID: "task", Title: "Task", Status: contracts.TaskStatusOpen},
		},
		Relations: []contracts.TaskRelation{
			{FromID: "task", ToID: "dep", Type: contracts.RelationDependsOn},
		},
	}

	graph, err := engine.BuildGraph(tree)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	err = engine.UpdateTaskStatus(graph, "task", contracts.TaskStatusClosed)
	if err == nil {
		t.Fatalf("UpdateTaskStatus() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "dependencies are not closed") {
		t.Fatalf("UpdateTaskStatus() error = %q, want dependency closure message", err)
	}

	if got := graph.Nodes["task"].Status; got != contracts.TaskStatusOpen {
		t.Fatalf("task node status = %q, want %q", got, contracts.TaskStatusOpen)
	}
	if got := graph.Nodes["task"].Task.Status; got != contracts.TaskStatusOpen {
		t.Fatalf("embedded task status = %q, want %q", got, contracts.TaskStatusOpen)
	}
}

func TestTaskEngineUpdateTaskStatusReturnsErrorWhenTaskNotFound(t *testing.T) {
	engine := NewTaskEngine()
	tree := &contracts.TaskTree{
		Root: contracts.Task{ID: "root", Status: contracts.TaskStatusClosed},
		Tasks: map[string]contracts.Task{
			"root": {ID: "root", Status: contracts.TaskStatusClosed},
			"task": {ID: "task", Title: "Task", Status: contracts.TaskStatusOpen},
		},
	}

	graph, err := engine.BuildGraph(tree)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	err = engine.UpdateTaskStatus(graph, "missing", contracts.TaskStatusClosed)
	if err == nil {
		t.Fatalf("UpdateTaskStatus() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), `task "missing" not found`) {
		t.Fatalf("UpdateTaskStatus() error = %q, want missing task message", err)
	}
}

func TestTaskEngineIsCompleteTreatsClosedFailedAndBlockedAsFinished(t *testing.T) {
	engine := NewTaskEngine()
	tree := &contracts.TaskTree{
		Root: contracts.Task{ID: "root", Status: contracts.TaskStatusClosed},
		Tasks: map[string]contracts.Task{
			"root": {ID: "root", Status: contracts.TaskStatusClosed},
			"a":    {ID: "a", Status: contracts.TaskStatusFailed},
			"b":    {ID: "b", Status: contracts.TaskStatusBlocked},
			"c":    {ID: "c", Status: contracts.TaskStatusClosed},
		},
	}

	graph, err := engine.BuildGraph(tree)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	if !engine.IsComplete(graph) {
		t.Fatalf("IsComplete() = false, want true")
	}
}

func TestTaskEngineConcurrentUpdateAndReadMaintainsConsistentAvailability(t *testing.T) {
	engine := NewTaskEngine()
	tree := &contracts.TaskTree{
		Root: contracts.Task{ID: "root", Status: contracts.TaskStatusClosed},
		Tasks: map[string]contracts.Task{
			"root":    {ID: "root", Status: contracts.TaskStatusClosed},
			"dep":     {ID: "dep", Status: contracts.TaskStatusOpen},
			"blocked": {ID: "blocked", Status: contracts.TaskStatusOpen},
		},
		Relations: []contracts.TaskRelation{
			{FromID: "blocked", ToID: "dep", Type: contracts.RelationDependsOn},
		},
	}

	graph, err := engine.BuildGraph(tree)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(2)

	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 2000; i++ {
			if err := engine.UpdateTaskStatus(graph, "dep", contracts.TaskStatusClosed); err != nil {
				t.Errorf("UpdateTaskStatus(dep, closed) error = %v", err)
				return
			}
			if err := engine.UpdateTaskStatus(graph, "dep", contracts.TaskStatusOpen); err != nil {
				t.Errorf("UpdateTaskStatus(dep, open) error = %v", err)
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 4000; i++ {
			got := summaryIDs(engine.GetNextAvailable(graph))
			if len(got) != 1 {
				t.Errorf("GetNextAvailable() = %v, want single stable task", got)
				return
			}
			if got[0] != "dep" && got[0] != "blocked" {
				t.Errorf("GetNextAvailable() = %v, want [dep] or [blocked]", got)
				return
			}
			if engine.IsComplete(graph) {
				t.Errorf("IsComplete() = true while open tasks remain")
				return
			}
		}
	}()

	close(start)
	wg.Wait()
}

func TestTaskEngineCalculateConcurrencyAcrossTopologies(t *testing.T) {
	engine := NewTaskEngine()
	if got := engine.CalculateConcurrency(nil, contracts.ConcurrencyOptions{}); got != 0 {
		t.Fatalf("CalculateConcurrency(nil) = %d, want 0", got)
	}
	if got := engine.CalculateConcurrency(&contracts.TaskGraph{}, contracts.ConcurrencyOptions{}); got != 0 {
		t.Fatalf("CalculateConcurrency(empty) = %d, want 0", got)
	}

	tests := []struct {
		name string
		tree *contracts.TaskTree
		opts contracts.ConcurrencyOptions
		want int
	}{
		{
			name: "linear chain",
			tree: &contracts.TaskTree{
				Root: contracts.Task{ID: "a", Status: contracts.TaskStatusOpen},
				Tasks: map[string]contracts.Task{
					"a": {ID: "a", Status: contracts.TaskStatusOpen},
					"b": {ID: "b", Status: contracts.TaskStatusOpen},
					"c": {ID: "c", Status: contracts.TaskStatusOpen},
				},
				Relations: []contracts.TaskRelation{
					{FromID: "b", ToID: "a", Type: contracts.RelationDependsOn},
					{FromID: "c", ToID: "b", Type: contracts.RelationDependsOn},
				},
			},
			want: 1,
		},
		{
			name: "diamond",
			tree: &contracts.TaskTree{
				Root: contracts.Task{ID: "a", Status: contracts.TaskStatusOpen},
				Tasks: map[string]contracts.Task{
					"a": {ID: "a", Status: contracts.TaskStatusOpen},
					"b": {ID: "b", Status: contracts.TaskStatusOpen},
					"c": {ID: "c", Status: contracts.TaskStatusOpen},
					"d": {ID: "d", Status: contracts.TaskStatusOpen},
				},
				Relations: []contracts.TaskRelation{
					{FromID: "b", ToID: "a", Type: contracts.RelationDependsOn},
					{FromID: "c", ToID: "a", Type: contracts.RelationDependsOn},
					{FromID: "d", ToID: "b", Type: contracts.RelationDependsOn},
					{FromID: "d", ToID: "c", Type: contracts.RelationDependsOn},
				},
			},
			want: 2,
		},
		{
			name: "fan out",
			tree: &contracts.TaskTree{
				Root: contracts.Task{ID: "a", Status: contracts.TaskStatusOpen},
				Tasks: map[string]contracts.Task{
					"a": {ID: "a", Status: contracts.TaskStatusOpen},
					"b": {ID: "b", Status: contracts.TaskStatusOpen},
					"c": {ID: "c", Status: contracts.TaskStatusOpen},
					"d": {ID: "d", Status: contracts.TaskStatusOpen},
					"e": {ID: "e", Status: contracts.TaskStatusOpen},
				},
				Relations: []contracts.TaskRelation{
					{FromID: "b", ToID: "a", Type: contracts.RelationDependsOn},
					{FromID: "c", ToID: "a", Type: contracts.RelationDependsOn},
					{FromID: "d", ToID: "a", Type: contracts.RelationDependsOn},
					{FromID: "e", ToID: "a", Type: contracts.RelationDependsOn},
				},
			},
			want: 4,
		},
		{
			name: "max workers limit",
			tree: &contracts.TaskTree{
				Root: contracts.Task{ID: "a", Status: contracts.TaskStatusOpen},
				Tasks: map[string]contracts.Task{
					"a": {ID: "a", Status: contracts.TaskStatusOpen},
					"b": {ID: "b", Status: contracts.TaskStatusOpen},
					"c": {ID: "c", Status: contracts.TaskStatusOpen},
					"d": {ID: "d", Status: contracts.TaskStatusOpen},
					"e": {ID: "e", Status: contracts.TaskStatusOpen},
				},
				Relations: []contracts.TaskRelation{
					{FromID: "b", ToID: "a", Type: contracts.RelationDependsOn},
					{FromID: "c", ToID: "a", Type: contracts.RelationDependsOn},
					{FromID: "d", ToID: "a", Type: contracts.RelationDependsOn},
					{FromID: "e", ToID: "a", Type: contracts.RelationDependsOn},
				},
			},
			opts: contracts.ConcurrencyOptions{MaxWorkers: 2},
			want: 2,
		},
		{
			name: "cpu limit uses 2x rule",
			tree: fanOutTaskTree("a", 20),
			opts: contracts.ConcurrencyOptions{
				CPUCount: 8,
			},
			want: 16,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			graph, err := engine.BuildGraph(tc.tree)
			if err != nil {
				t.Fatalf("BuildGraph() error = %v", err)
			}
			got := engine.CalculateConcurrency(graph, tc.opts)
			if got != tc.want {
				t.Fatalf("CalculateConcurrency() = %d, want %d", got, tc.want)
			}
		})
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

func summaryIDs(tasks []contracts.TaskSummary) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

func fanOutTaskTree(rootID string, fanOut int) *contracts.TaskTree {
	tasks := map[string]contracts.Task{
		rootID: {ID: rootID, Status: contracts.TaskStatusOpen},
	}
	relations := make([]contracts.TaskRelation, 0, fanOut)
	for i := 0; i < fanOut; i++ {
		id := fmt.Sprintf("n-%02d", i)
		tasks[id] = contracts.Task{ID: id, Status: contracts.TaskStatusOpen}
		relations = append(relations, contracts.TaskRelation{
			FromID: id,
			ToID:   rootID,
			Type:   contracts.RelationDependsOn,
		})
	}
	return &contracts.TaskTree{
		Root:      tasks[rootID],
		Tasks:     tasks,
		Relations: relations,
	}
}
