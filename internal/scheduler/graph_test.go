package scheduler

import (
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTaskGraphBuildsReverseDependenciesDeterministically(t *testing.T) {
	graph, err := NewTaskGraph([]TaskNode{
		{ID: "3", State: TaskStatePending, DependsOn: []string{"1"}},
		{ID: "1", State: TaskStateSucceeded},
		{ID: "2", State: TaskStatePending, DependsOn: []string{"1"}},
		{ID: "4", State: TaskStatePending, DependsOn: []string{"2", "3"}},
	})
	if err != nil {
		t.Fatalf("NewTaskGraph() error = %v", err)
	}

	if got := graph.DependenciesOf("4"); !reflect.DeepEqual(got, []string{"2", "3"}) {
		t.Fatalf("DependenciesOf(4) = %v, want [2 3]", got)
	}

	if got := graph.DependentsOf("1"); !reflect.DeepEqual(got, []string{"2", "3"}) {
		t.Fatalf("DependentsOf(1) = %v, want [2 3]", got)
	}
}

func TestTaskGraphReadySetIsDeterministic(t *testing.T) {
	graph, err := NewTaskGraph([]TaskNode{
		{ID: "7", State: TaskStatePending, DependsOn: []string{"6"}},
		{ID: "5", State: TaskStatePending},
		{ID: "4", State: TaskStatePending, DependsOn: []string{"2", "3"}},
		{ID: "3", State: TaskStatePending, DependsOn: []string{"1"}},
		{ID: "6", State: TaskStateFailed},
		{ID: "2", State: TaskStatePending, DependsOn: []string{"1"}},
		{ID: "1", State: TaskStateSucceeded},
	})
	if err != nil {
		t.Fatalf("NewTaskGraph() error = %v", err)
	}

	if got := graph.ReadySet(); !reflect.DeepEqual(got, []string{"2", "3", "5"}) {
		t.Fatalf("ReadySet() = %v, want [2 3 5]", got)
	}
}

func TestTaskStateTerminalBehavior(t *testing.T) {
	tests := []struct {
		name  string
		state TaskState
		want  bool
	}{
		{name: "pending is not terminal", state: TaskStatePending, want: false},
		{name: "running is not terminal", state: TaskStateRunning, want: false},
		{name: "succeeded is terminal", state: TaskStateSucceeded, want: true},
		{name: "failed is terminal", state: TaskStateFailed, want: true},
		{name: "canceled is terminal", state: TaskStateCanceled, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.state.IsTerminal(); got != tc.want {
				t.Fatalf("%s.IsTerminal() = %v, want %v", tc.state, got, tc.want)
			}
		})
	}
}

func TestTaskGraphInspectNodeReportsDeterministicReadinessAndTerminalState(t *testing.T) {
	graph, err := NewTaskGraph([]TaskNode{
		{ID: "1", State: TaskStateSucceeded},
		{ID: "2", State: TaskStatePending, DependsOn: []string{"1"}},
		{ID: "3", State: TaskStateRunning, DependsOn: []string{"1"}},
		{ID: "4", State: TaskStateFailed, DependsOn: []string{"2", "3"}},
	})
	if err != nil {
		t.Fatalf("NewTaskGraph() error = %v", err)
	}

	testCases := []struct {
		taskID string
		want   NodeInspection
	}{
		{
			taskID: "2",
			want: NodeInspection{
				TaskID:     "2",
				State:      TaskStatePending,
				Ready:      true,
				Terminal:   false,
				DependsOn:  []string{"1"},
				Dependents: []string{"4"},
			},
		},
		{
			taskID: "4",
			want: NodeInspection{
				TaskID:     "4",
				State:      TaskStateFailed,
				Ready:      false,
				Terminal:   true,
				DependsOn:  []string{"2", "3"},
				Dependents: nil,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.taskID, func(t *testing.T) {
			got, err := graph.InspectNode(tc.taskID)
			if err != nil {
				t.Fatalf("InspectNode(%s) error = %v", tc.taskID, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("InspectNode(%s) = %+v, want %+v", tc.taskID, got, tc.want)
			}
		})
	}
}

func TestTaskGraphReserveReadyGatesDependentTasksUntilAllDependenciesSucceed(t *testing.T) {
	graph, err := NewTaskGraph([]TaskNode{
		{ID: "1", State: TaskStatePending},
		{ID: "2", State: TaskStatePending},
		{ID: "3", State: TaskStatePending, DependsOn: []string{"1", "2"}},
	})
	if err != nil {
		t.Fatalf("NewTaskGraph() error = %v", err)
	}

	if got := graph.ReserveReady(10); !reflect.DeepEqual(got, []string{"1", "2"}) {
		t.Fatalf("ReserveReady() = %v, want [1 2]", got)
	}

	if got := graph.ReserveReady(10); len(got) != 0 {
		t.Fatalf("ReserveReady() should not return running tasks, got %v", got)
	}

	if err := graph.SetState("1", TaskStateSucceeded); err != nil {
		t.Fatalf("SetState(1) error = %v", err)
	}
	if got := graph.ReserveReady(10); len(got) != 0 {
		t.Fatalf("ReserveReady() should gate task 3 until all deps complete, got %v", got)
	}

	if err := graph.SetState("2", TaskStateSucceeded); err != nil {
		t.Fatalf("SetState(2) error = %v", err)
	}
	if got := graph.ReserveReady(10); !reflect.DeepEqual(got, []string{"3"}) {
		t.Fatalf("ReserveReady() = %v, want [3]", got)
	}
}

func TestTaskGraphReserveReadyStress_NoDuplicateReservations(t *testing.T) {
	nodes := make([]TaskNode, 0, 120)
	for i := 0; i < 120; i++ {
		nodes = append(nodes, TaskNode{ID: taskID(i), State: TaskStatePending})
	}
	graph, err := NewTaskGraph(nodes)
	if err != nil {
		t.Fatalf("NewTaskGraph() error = %v", err)
	}

	var seenMu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				reserved := graph.ReserveReady(1)
				if len(reserved) == 0 {
					return
				}
				task := reserved[0]
				seenMu.Lock()
				seen[task]++
				seenMu.Unlock()
				if err := graph.SetState(task, TaskStateSucceeded); err != nil {
					t.Errorf("SetState(%s) error = %v", task, err)
					return
				}
				time.Sleep(time.Microsecond)
			}
		}()
	}
	wg.Wait()

	if len(seen) != 120 {
		t.Fatalf("expected 120 unique reservations, got %d", len(seen))
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("expected task %s reserved once, got %d", id, count)
		}
	}
}

func TestTaskGraphReserveReadyStress_RespectsDAGDependencies(t *testing.T) {
	graph, err := NewTaskGraph([]TaskNode{
		{ID: "a", State: TaskStatePending},
		{ID: "b", State: TaskStatePending},
		{ID: "c", State: TaskStatePending},
		{ID: "d", State: TaskStatePending, DependsOn: []string{"a", "b"}},
		{ID: "e", State: TaskStatePending, DependsOn: []string{"b", "c"}},
		{ID: "f", State: TaskStatePending, DependsOn: []string{"d", "e"}},
	})
	if err != nil {
		t.Fatalf("NewTaskGraph() error = %v", err)
	}

	var orderMu sync.Mutex
	order := []string{}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				reserved := graph.ReserveReady(1)
				if len(reserved) == 0 {
					if len(graph.ReadySet()) == 0 {
						return
					}
					continue
				}
				task := reserved[0]
				deps := graph.DependenciesOf(task)
				for _, dep := range deps {
					inspection, inspectErr := graph.InspectNode(dep)
					if inspectErr != nil {
						t.Errorf("InspectNode(%s) error = %v", dep, inspectErr)
						return
					}
					if inspection.State != TaskStateSucceeded {
						t.Errorf("reserved %s before dependency %s succeeded (state=%s)", task, dep, inspection.State)
						return
					}
				}
				orderMu.Lock()
				order = append(order, task)
				orderMu.Unlock()
				if err := graph.SetState(task, TaskStateSucceeded); err != nil {
					t.Errorf("SetState(%s) error = %v", task, err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if len(order) != 6 {
		t.Fatalf("expected 6 tasks reserved, got %d (%v)", len(order), order)
	}
}

func TestTaskGraphRejectsCircularDependenciesWithCyclePath(t *testing.T) {
	_, err := NewTaskGraph([]TaskNode{
		{ID: "a", State: TaskStatePending, DependsOn: []string{"c"}},
		{ID: "b", State: TaskStatePending, DependsOn: []string{"a"}},
		{ID: "c", State: TaskStatePending, DependsOn: []string{"b"}},
	})
	if err == nil {
		t.Fatalf("expected circular dependency error, got nil")
	}
	if !strings.Contains(err.Error(), "circular dependency") {
		t.Fatalf("expected circular dependency error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "a -> c -> b -> a") {
		t.Fatalf("expected cycle path in error, got %q", err.Error())
	}
}

func TestTaskGraphRejectsSelfDependencyWithCyclePath(t *testing.T) {
	_, err := NewTaskGraph([]TaskNode{
		{ID: "a", State: TaskStatePending, DependsOn: []string{"a"}},
	})
	if err == nil {
		t.Fatalf("expected circular dependency error, got nil")
	}
	if !strings.Contains(err.Error(), "circular dependency") {
		t.Fatalf("expected circular dependency error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "a -> a") {
		t.Fatalf("expected self-cycle path in error, got %q", err.Error())
	}
}

func TestTaskGraphBuildGraphGetNextAvailableUpdateTaskStatus_Topologies(t *testing.T) {
	type transition struct {
		taskID    string
		state     TaskState
		wantReady []string
	}

	tests := []struct {
		name         string
		nodes        []TaskNode
		initialReady []string
		transitions  []transition
	}{
		{
			name: "linear chain",
			nodes: []TaskNode{
				{ID: "a", State: TaskStatePending},
				{ID: "b", State: TaskStatePending, DependsOn: []string{"a"}},
				{ID: "c", State: TaskStatePending, DependsOn: []string{"b"}},
			},
			initialReady: []string{"a"},
			transitions: []transition{
				{taskID: "a", state: TaskStateSucceeded, wantReady: []string{"b"}},
				{taskID: "b", state: TaskStateSucceeded, wantReady: []string{"c"}},
				{taskID: "c", state: TaskStateSucceeded, wantReady: []string{}},
			},
		},
		{
			name: "diamond",
			nodes: []TaskNode{
				{ID: "a", State: TaskStatePending},
				{ID: "b", State: TaskStatePending, DependsOn: []string{"a"}},
				{ID: "c", State: TaskStatePending, DependsOn: []string{"a"}},
				{ID: "d", State: TaskStatePending, DependsOn: []string{"b", "c"}},
			},
			initialReady: []string{"a"},
			transitions: []transition{
				{taskID: "a", state: TaskStateSucceeded, wantReady: []string{"b", "c"}},
				{taskID: "b", state: TaskStateSucceeded, wantReady: []string{"c"}},
				{taskID: "c", state: TaskStateSucceeded, wantReady: []string{"d"}},
				{taskID: "d", state: TaskStateSucceeded, wantReady: []string{}},
			},
		},
		{
			name: "fan-out",
			nodes: []TaskNode{
				{ID: "a", State: TaskStatePending},
				{ID: "b", State: TaskStatePending, DependsOn: []string{"a"}},
				{ID: "c", State: TaskStatePending, DependsOn: []string{"a"}},
				{ID: "d", State: TaskStatePending, DependsOn: []string{"a"}},
				{ID: "e", State: TaskStatePending, DependsOn: []string{"a"}},
			},
			initialReady: []string{"a"},
			transitions: []transition{
				{taskID: "a", state: TaskStateSucceeded, wantReady: []string{"b", "c", "d", "e"}},
				{taskID: "b", state: TaskStateSucceeded, wantReady: []string{"c", "d", "e"}},
				{taskID: "c", state: TaskStateSucceeded, wantReady: []string{"d", "e"}},
				{taskID: "d", state: TaskStateSucceeded, wantReady: []string{"e"}},
				{taskID: "e", state: TaskStateSucceeded, wantReady: []string{}},
			},
		},
		{
			name: "fan-in",
			nodes: []TaskNode{
				{ID: "a", State: TaskStatePending},
				{ID: "b", State: TaskStatePending},
				{ID: "c", State: TaskStatePending},
				{ID: "d", State: TaskStatePending},
				{ID: "e", State: TaskStatePending, DependsOn: []string{"a", "b", "c", "d"}},
			},
			initialReady: []string{"a", "b", "c", "d"},
			transitions: []transition{
				{taskID: "a", state: TaskStateSucceeded, wantReady: []string{"b", "c", "d"}},
				{taskID: "b", state: TaskStateSucceeded, wantReady: []string{"c", "d"}},
				{taskID: "c", state: TaskStateSucceeded, wantReady: []string{"d"}},
				{taskID: "d", state: TaskStateSucceeded, wantReady: []string{"e"}},
				{taskID: "e", state: TaskStateSucceeded, wantReady: []string{}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			graph, err := NewTaskGraph(tc.nodes)
			if err != nil {
				t.Fatalf("NewTaskGraph() error = %v", err)
			}
			if len(graph.nodes) != len(tc.nodes) {
				t.Fatalf("NewTaskGraph() built %d nodes, want %d", len(graph.nodes), len(tc.nodes))
			}

			if got := graph.ReadySet(); !reflect.DeepEqual(got, tc.initialReady) {
				t.Fatalf("ReadySet() initial = %v, want %v", got, tc.initialReady)
			}

			for _, step := range tc.transitions {
				if err := graph.SetState(step.taskID, step.state); err != nil {
					t.Fatalf("SetState(%s) error = %v", step.taskID, err)
				}
				if got := graph.ReadySet(); !reflect.DeepEqual(got, step.wantReady) {
					t.Fatalf("ReadySet() after %s=%s = %v, want %v", step.taskID, step.state, got, step.wantReady)
				}
			}
		})
	}
}

func TestTaskGraphCalculateConcurrencyAcrossTopologies(t *testing.T) {
	tests := []struct {
		name  string
		nodes []TaskNode
		want  int
	}{
		{
			name: "empty graph",
			want: 0,
		},
		{
			name: "linear chain",
			nodes: []TaskNode{
				{ID: "a", State: TaskStatePending},
				{ID: "b", State: TaskStatePending, DependsOn: []string{"a"}},
				{ID: "c", State: TaskStatePending, DependsOn: []string{"b"}},
			},
			want: 1,
		},
		{
			name: "diamond",
			nodes: []TaskNode{
				{ID: "a", State: TaskStatePending},
				{ID: "b", State: TaskStatePending, DependsOn: []string{"a"}},
				{ID: "c", State: TaskStatePending, DependsOn: []string{"a"}},
				{ID: "d", State: TaskStatePending, DependsOn: []string{"b", "c"}},
			},
			want: 2,
		},
		{
			name: "fan-out",
			nodes: []TaskNode{
				{ID: "a", State: TaskStatePending},
				{ID: "b", State: TaskStatePending, DependsOn: []string{"a"}},
				{ID: "c", State: TaskStatePending, DependsOn: []string{"a"}},
				{ID: "d", State: TaskStatePending, DependsOn: []string{"a"}},
				{ID: "e", State: TaskStatePending, DependsOn: []string{"a"}},
			},
			want: 4,
		},
		{
			name: "fan-in",
			nodes: []TaskNode{
				{ID: "a", State: TaskStatePending},
				{ID: "b", State: TaskStatePending},
				{ID: "c", State: TaskStatePending},
				{ID: "d", State: TaskStatePending},
				{ID: "e", State: TaskStatePending, DependsOn: []string{"a", "b", "c", "d"}},
			},
			want: 4,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			graph, err := NewTaskGraph(tc.nodes)
			if err != nil {
				t.Fatalf("NewTaskGraph() error = %v", err)
			}
			if got := callTaskGraphCalculateConcurrency(t, &graph); got != tc.want {
				t.Fatalf("CalculateConcurrency() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestTaskGraphIsCompleteReturnsTrueWhenAllTasksFinished(t *testing.T) {
	graph, err := NewTaskGraph([]TaskNode{
		{ID: "a", State: TaskStatePending},
		{ID: "b", State: TaskStatePending},
		{ID: "c", State: TaskStatePending, DependsOn: []string{"a", "b"}},
	})
	if err != nil {
		t.Fatalf("NewTaskGraph() error = %v", err)
	}

	if callTaskGraphIsComplete(t, &graph) {
		t.Fatalf("IsComplete() = true for pending graph, want false")
	}

	if err := graph.SetState("a", TaskStateSucceeded); err != nil {
		t.Fatalf("SetState(a) error = %v", err)
	}
	if err := graph.SetState("b", TaskStateFailed); err != nil {
		t.Fatalf("SetState(b) error = %v", err)
	}
	if err := graph.SetState("c", TaskStateRunning); err != nil {
		t.Fatalf("SetState(c) error = %v", err)
	}
	if callTaskGraphIsComplete(t, &graph) {
		t.Fatalf("IsComplete() = true with running task, want false")
	}

	if err := graph.SetState("c", TaskStateCanceled); err != nil {
		t.Fatalf("SetState(c) error = %v", err)
	}
	if !callTaskGraphIsComplete(t, &graph) {
		t.Fatalf("IsComplete() = false after all tasks reached terminal states, want true")
	}
}

func callTaskGraphCalculateConcurrency(t *testing.T, graph *TaskGraph) int {
	t.Helper()
	method := reflect.ValueOf(graph).MethodByName("CalculateConcurrency")
	if !method.IsValid() {
		t.Fatalf("TaskGraph.CalculateConcurrency is not implemented")
	}
	results := method.Call(nil)
	if len(results) != 1 || results[0].Kind() != reflect.Int {
		t.Fatalf("TaskGraph.CalculateConcurrency() returned unexpected signature")
	}
	return int(results[0].Int())
}

func callTaskGraphIsComplete(t *testing.T, graph *TaskGraph) bool {
	t.Helper()
	method := reflect.ValueOf(graph).MethodByName("IsComplete")
	if !method.IsValid() {
		t.Fatalf("TaskGraph.IsComplete is not implemented")
	}
	results := method.Call(nil)
	if len(results) != 1 || results[0].Kind() != reflect.Bool {
		t.Fatalf("TaskGraph.IsComplete() returned unexpected signature")
	}
	return results[0].Bool()
}

func taskID(i int) string {
	return "task-" + strconv.Itoa(i)
}
