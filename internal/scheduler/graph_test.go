package scheduler

import (
	"reflect"
	"testing"
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
