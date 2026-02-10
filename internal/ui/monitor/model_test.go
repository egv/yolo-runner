package monitor

import (
	"fmt"
	"testing"
	"time"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

func TestModelTracksCurrentTaskPhaseAgeAndHistory(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 0, 10, 0, time.UTC)
	model := NewModel(func() time.Time { return now })

	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-1", TaskTitle: "Readable task", Message: "started", Timestamp: now.Add(-5 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeRunnerStarted, TaskID: "task-1", Message: "runner started", Timestamp: now.Add(-3 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeRunnerFinished, TaskID: "task-1", Message: "runner finished", Timestamp: now.Add(-1 * time.Second)})

	view := model.View()
	assertContains(t, view, "Current Task: task-1 - Readable task")
	assertContains(t, view, "Phase: runner_finished")
	assertContains(t, view, "Last Output Age: 1s")
	assertContains(t, view, "task-1 - Readable task")
	assertContains(t, view, "runner started")
	assertContains(t, view, "runner finished")
}

func TestModelRendersWorkerLanesFromParallelContext(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 1, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })

	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-1", TaskTitle: "First", WorkerID: "worker-1", QueuePos: 1, Timestamp: now.Add(-3 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeRunnerStarted, TaskID: "task-2", TaskTitle: "Second", WorkerID: "worker-2", QueuePos: 2, Timestamp: now.Add(-2 * time.Second)})

	view := model.View()
	assertContains(t, view, "Workers:")
	assertContains(t, view, "worker-1 => task-1 - First [task_started] (queue=1)")
	assertContains(t, view, "worker-2 => task-2 - Second [runner_started] (queue=2)")
}

func TestModelRendersLandingQueueStatesFromTaskFinishedEvents(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 2, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })

	model.Apply(contracts.Event{Type: contracts.EventTypeTaskFinished, TaskID: "task-1", TaskTitle: "First", Message: "closed", Timestamp: now.Add(-3 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskFinished, TaskID: "task-2", TaskTitle: "Second", Message: "failed", Timestamp: now.Add(-1 * time.Second)})

	view := model.View()
	assertContains(t, view, "Landing Queue:")
	assertContains(t, view, "task-1 - First => closed")
	assertContains(t, view, "task-2 - Second => failed")
}

func TestModelNormalizesTriagePanelData(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 3, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })

	model.Apply(contracts.Event{
		Type:      contracts.EventTypeTaskDataUpdated,
		TaskID:    "task-1",
		TaskTitle: "First",
		Metadata: map[string]string{
			"triage_status": " Failed ",
			"triage_reason": "  lint failed  ",
		},
		Timestamp: now.Add(-2 * time.Second),
	})

	view := model.View()
	assertContains(t, view, "Triage:")
	assertContains(t, view, "task-1 - First => failed | lint failed")
}

func TestModelStoresRunParametersFromRunStartedEvent(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 4, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })

	model.Apply(contracts.Event{
		Type: contracts.EventTypeRunStarted,
		Metadata: map[string]string{
			"root_id":                "yr-2y0b",
			"concurrency":            "2",
			"model":                  "openai/gpt-5.3-codex",
			"runner_timeout":         "15m0s",
			"stream":                 "true",
			"verbose_stream":         "false",
			"stream_output_interval": "150ms",
			"stream_output_buffer":   "64",
		},
		Timestamp: now.Add(-3 * time.Second),
	})

	view := model.View()
	assertContains(t, view, "Run Parameters:")
	assertContains(t, view, "root_id=yr-2y0b")
	assertContains(t, view, "concurrency=2")
	assertContains(t, view, "runner_timeout=15m0s")
}

func TestModelBuildsDerivedRunWorkerTaskState(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 5, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })

	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-1", TaskTitle: "First", WorkerID: "worker-0", QueuePos: 1, Timestamp: now.Add(-4 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeRunnerStarted, TaskID: "task-1", TaskTitle: "First", WorkerID: "worker-0", QueuePos: 1, Timestamp: now.Add(-3 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeRunnerFinished, TaskID: "task-1", TaskTitle: "First", WorkerID: "worker-0", Message: "completed", Timestamp: now.Add(-1 * time.Second)})

	state := model.Snapshot()
	if state.Root.Workers["worker-0"].CurrentTaskID != "task-1" {
		t.Fatalf("expected worker current task, got %#v", state.Root.Workers["worker-0"])
	}
	task := state.Root.Tasks["task-1"]
	if task.TaskID != "task-1" || task.Title != "First" {
		t.Fatalf("unexpected task snapshot %#v", task)
	}
	if task.RunnerPhase != "runner_finished" {
		t.Fatalf("expected derived runner phase runner_finished, got %q", task.RunnerPhase)
	}
}

func TestModelDerivesRunnerCommandAndOutputSummaries(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 6, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })

	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-2", TaskTitle: "Second", WorkerID: "worker-1", Timestamp: now.Add(-5 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeRunnerCommandStarted, TaskID: "task-2", WorkerID: "worker-1", Message: "go test ./...", Timestamp: now.Add(-4 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeRunnerOutput, TaskID: "task-2", WorkerID: "worker-1", Message: "ok", Timestamp: now.Add(-3 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeRunnerCommandFinished, TaskID: "task-2", WorkerID: "worker-1", Message: "exit=0", Timestamp: now.Add(-2 * time.Second)})

	task := model.Snapshot().Root.Tasks["task-2"]
	if task.CommandStartedCount != 1 || task.CommandFinishedCount != 1 {
		t.Fatalf("expected command counters to be derived, got %#v", task)
	}
	if task.OutputCount != 1 {
		t.Fatalf("expected output counter to be derived, got %#v", task)
	}
	if task.LastCommandSummary != "go test ./... -> exit=0" {
		t.Fatalf("expected command summary, got %#v", task)
	}
}

func TestModelDerivesWarningLifecycleAsActiveThenResolved(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 7, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })

	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-3", TaskTitle: "Third", WorkerID: "worker-2", Timestamp: now.Add(-5 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeRunnerWarning, TaskID: "task-3", WorkerID: "worker-2", Message: "no output for 5m", Timestamp: now.Add(-4 * time.Second)})
	warnTask := model.Snapshot().Root.Tasks["task-3"]
	if !warnTask.WarningActive {
		t.Fatalf("expected warning lifecycle to be active, got %#v", warnTask)
	}
	if warnTask.WarningCount != 1 {
		t.Fatalf("expected warning count=1, got %#v", warnTask)
	}

	model.Apply(contracts.Event{Type: contracts.EventTypeRunnerFinished, TaskID: "task-3", WorkerID: "worker-2", Message: "completed", Timestamp: now.Add(-2 * time.Second)})
	finishedTask := model.Snapshot().Root.Tasks["task-3"]
	if finishedTask.WarningActive {
		t.Fatalf("expected warning lifecycle resolved on runner finish, got %#v", finishedTask)
	}
	if finishedTask.TerminalStatus != "completed" {
		t.Fatalf("expected terminal status from runner finished message, got %#v", finishedTask)
	}
}

func TestModelRendersStatusBarMetrics(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 8, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })

	model.Apply(contracts.Event{Type: contracts.EventTypeRunStarted, Metadata: map[string]string{"root_id": "yr-2y0b"}, Timestamp: now.Add(-10 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-1", TaskTitle: "First", WorkerID: "worker-0", QueuePos: 1, Timestamp: now.Add(-9 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeRunnerFinished, TaskID: "task-1", WorkerID: "worker-0", Message: "completed", Timestamp: now.Add(-8 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskFinished, TaskID: "task-1", TaskTitle: "First", Message: "closed", Timestamp: now.Add(-7 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-2", TaskTitle: "Second", WorkerID: "worker-1", QueuePos: 2, Timestamp: now.Add(-6 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeRunnerWarning, TaskID: "task-2", WorkerID: "worker-1", Message: "no output", Timestamp: now.Add(-5 * time.Second)})

	view := model.View()
	assertContains(t, view, "Status Bar:")
	assertContains(t, view, "runtime=10s")
	assertContains(t, view, "activity=active")
	assertContains(t, view, "completed=1")
	assertContains(t, view, "in_progress=1")
	assertContains(t, view, "blocked=0")
	assertContains(t, view, "failed=0")
	assertContains(t, view, "total=2")
	assertContains(t, view, "queue_depth=1")
	assertContains(t, view, "worker_utilization=50%")
	assertContains(t, view, "throughput=")
	assertContains(t, view, "errors=run:warning workers:warning tasks:warning")
}

func TestModelSupportsHierarchicalPanelNavigationWithArrowAndVimKeys(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 9, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })

	model.Apply(contracts.Event{Type: contracts.EventTypeRunStarted, Metadata: map[string]string{"root_id": "yr-2y0b"}, Timestamp: now.Add(-10 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-1", TaskTitle: "First", WorkerID: "worker-0", QueuePos: 1, Timestamp: now.Add(-9 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeRunnerWarning, TaskID: "task-1", WorkerID: "worker-0", Message: "stalled", Timestamp: now.Add(-8 * time.Second)})

	view := model.View()
	assertContains(t, view, "Panels:")
	assertContains(t, view, "> [-] Run")

	model.HandleKey("down")
	view = model.View()
	assertContains(t, view, "> [-] Workers")

	model.HandleKey("j")
	view = model.View()
	assertContains(t, view, "> [+] worker-0")

	model.HandleKey("up")
	model.HandleKey("k")
	view = model.View()
	assertContains(t, view, "> [-] Run")
}

func TestModelSupportsExpandCollapseViaEnterSpaceAndVimKeys(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 10, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })

	model.Apply(contracts.Event{Type: contracts.EventTypeRunStarted, Metadata: map[string]string{"root_id": "yr-2y0b"}, Timestamp: now.Add(-10 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-1", TaskTitle: "First", WorkerID: "worker-0", QueuePos: 1, Timestamp: now.Add(-9 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeRunnerWarning, TaskID: "task-1", WorkerID: "worker-0", Message: "stalled", Timestamp: now.Add(-8 * time.Second)})

	model.HandleKey("down")
	model.HandleKey("space")
	view := model.View()
	assertContains(t, view, "> [+] Workers severity=warning")

	model.HandleKey("l")
	view = model.View()
	assertContains(t, view, "> [-] Workers severity=warning")
	assertContains(t, view, "[+] worker-0 severity=warning")

	model.HandleKey("down")
	model.HandleKey("enter")
	view = model.View()
	assertContains(t, view, "> [-] worker-0 severity=warning")
	assertContains(t, view, "task-1 - First severity=warning")

	model.HandleKey("h")
	view = model.View()
	assertContains(t, view, "> [+] worker-0 severity=warning")
}

func TestModelBoundsHistoryWithPerformanceControls(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 11, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })
	model.SetPerformanceControls(5, 256)

	for i := 0; i < 20; i++ {
		model.Apply(contracts.Event{
			Type:      contracts.EventTypeRunnerOutput,
			TaskID:    "task-1",
			TaskTitle: "First",
			Message:   fmt.Sprintf("line-%02d", i),
			Timestamp: now.Add(time.Duration(i) * time.Second),
		})
	}

	perf := model.PerformanceSnapshot()
	if perf.HistorySize != 5 {
		t.Fatalf("expected bounded history size 5, got %#v", perf)
	}

	view := model.View()
	assertContains(t, view, "line-19")
	assertNotContains(t, view, "line-00")
}

func TestModelUsesViewportAwareRenderingForLargePanelTrees(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 12, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })
	model.SetPerformanceControls(256, 120)
	model.SetViewportHeight(12)

	model.Apply(contracts.Event{Type: contracts.EventTypeRunStarted, Metadata: map[string]string{"root_id": "yr-2y0b"}, Timestamp: now.Add(-10 * time.Second)})
	for i := 0; i < 500; i++ {
		workerID := fmt.Sprintf("worker-%d", i%10)
		model.Apply(contracts.Event{
			Type:      contracts.EventTypeTaskStarted,
			TaskID:    fmt.Sprintf("task-%03d", i),
			TaskTitle: fmt.Sprintf("Task %03d", i),
			WorkerID:  workerID,
			QueuePos:  i + 1,
			Timestamp: now.Add(time.Duration(i) * time.Millisecond),
		})
	}

	model.panelExpand["tasks"] = true
	view := model.View()
	perf := model.PerformanceSnapshot()

	if perf.TotalPanelRows != 120 {
		t.Fatalf("expected bounded panel rows to 120, got %#v", perf)
	}
	if perf.VisiblePanelRows > 12 {
		t.Fatalf("expected viewport to cap visible rows, got %#v", perf)
	}
	if !perf.PanelRowsTruncated {
		t.Fatalf("expected panel truncation marker, got %#v", perf)
	}
	assertContains(t, view, "Performance:")
	assertContains(t, view, "panel_rows=12/120")
	assertContains(t, view, "... 108 more panel rows")
}

func assertContains(t *testing.T, text string, expected string) {
	t.Helper()
	if !contains(text, expected) {
		t.Fatalf("expected %q in %q", expected, text)
	}
}

func assertNotContains(t *testing.T, text string, expected string) {
	t.Helper()
	if contains(text, expected) {
		t.Fatalf("did not expect %q in %q", expected, text)
	}
}

func contains(text string, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(text); i++ {
		if text[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
