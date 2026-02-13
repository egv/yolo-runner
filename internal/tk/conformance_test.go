package tk

import (
	"testing"

	"github.com/anomalyco/yolo-runner/internal/contracts/conformance"
)

func TestTaskManagerConformance(t *testing.T) {
	conformance.RunTaskManagerSuite(t, conformance.TaskManagerConfig{
		Backend:        "tk",
		NewTaskManager: newTaskManagerConformanceFixture,
	})
}

func newTaskManagerConformanceFixture(t *testing.T, scenario conformance.TaskManagerScenario) conformance.TaskManagerFixture {
	t.Helper()

	switch scenario {
	case conformance.TaskManagerScenarioTaskSelection:
		r := &fakeRunner{responses: map[string]string{
			"tk query": `{"id":"root","status":"open","type":"epic","priority":0}` + "\n" +
				`{"id":"root.1","status":"open","type":"task","priority":1,"parent":"root","title":"Blocked by dep","deps":["dep.1"]}` + "\n" +
				`{"id":"root.2","status":"open","type":"task","priority":0,"parent":"root","title":"Ready now"}` + "\n" +
				`{"id":"root.3","status":"open","type":"task","priority":2,"parent":"root","title":"Lower priority"}` + "\n" +
				`{"id":"dep.1","status":"open","type":"task","priority":0,"title":"Dependency"}`,
			"tk ready": "root.3 [open] - Lower priority\nroot.2 [open] - Ready now\nroot.1 [open] - Blocked by dep\n",
		}}
		return conformance.TaskManagerFixture{
			Manager: NewTaskManager(r),
			Assert: func(t *testing.T) {
				t.Helper()
				if !r.called("tk ready") || !r.called("tk query") {
					t.Fatalf("expected tk ready and tk query calls, got %v", r.calls)
				}
			},
		}
	case conformance.TaskManagerScenarioGetTaskDetails:
		r := &fakeRunner{responses: map[string]string{
			"tk show t-1": "# Task 1\n",
			"tk query": `{"id":"t-1","status":"open","type":"task","title":"Task 1","description":"do work","deps":["d-1","d-2"]}` + "\n" +
				`{"id":"d-1","status":"closed","type":"task","title":"Dep 1"}` + "\n" +
				`{"id":"d-2","status":"open","type":"task","title":"Dep 2"}`,
		}}
		return conformance.TaskManagerFixture{Manager: NewTaskManager(r)}
	case conformance.TaskManagerScenarioTerminalStateTransitions:
		r := &fakeRunner{responses: map[string]string{
			"tk query": `{"id":"root","status":"open","type":"epic","priority":0}` + "\n" +
				`{"id":"root.1","status":"open","type":"task","priority":1,"parent":"root","title":"A"}` + "\n" +
				`{"id":"root.2","status":"open","type":"task","priority":2,"parent":"root","title":"B"}`,
			"tk ready": "root.1 [open] - A\nroot.2 [open] - B\n",
		}}
		return conformance.TaskManagerFixture{
			Manager: NewTaskManager(r),
			Assert: func(t *testing.T) {
				t.Helper()
				if !r.called("tk status root.1 open") {
					t.Fatalf("expected blocked/failed status to map to tk status open, got %v", r.calls)
				}
				if countCalls(r.calls, "tk reopen root.1") < 2 {
					t.Fatalf("expected task reopen command to run twice, got %v", r.calls)
				}
			},
		}
	case conformance.TaskManagerScenarioStatusLifecycle:
		r := &fakeRunner{responses: map[string]string{}}
		return conformance.TaskManagerFixture{
			Manager: NewTaskManager(r),
			Assert: func(t *testing.T) {
				t.Helper()
				required := []string{
					"tk start t-1",
					"tk close t-1",
					"tk status t-1 open",
					"tk reopen t-1",
				}
				for _, cmd := range required {
					if !r.called(cmd) {
						t.Fatalf("expected command %q in status lifecycle, got %v", cmd, r.calls)
					}
				}
			},
		}
	case conformance.TaskManagerScenarioSetTaskData:
		r := &fakeRunner{responses: map[string]string{}}
		return conformance.TaskManagerFixture{
			Manager: NewTaskManager(r),
			Assert: func(t *testing.T) {
				t.Helper()
				reasonIdx := callIndex(r.calls, "tk add-note t-1 triage_reason=timeout")
				statusIdx := callIndex(r.calls, "tk add-note t-1 triage_status=blocked")
				if reasonIdx == -1 || statusIdx == -1 {
					t.Fatalf("expected set data note commands, got %v", r.calls)
				}
				if reasonIdx > statusIdx {
					t.Fatalf("expected deterministic key ordering in notes, got %v", r.calls)
				}
			},
		}
	default:
		t.Fatalf("unsupported scenario %q", scenario)
		return conformance.TaskManagerFixture{}
	}
}

func callIndex(calls []string, target string) int {
	for idx, call := range calls {
		if call == target {
			return idx
		}
	}
	return -1
}

func countCalls(calls []string, target string) int {
	count := 0
	for _, call := range calls {
		if call == target {
			count++
		}
	}
	return count
}
