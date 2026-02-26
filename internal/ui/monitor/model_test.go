package monitor

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
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

func TestModelSurfacesTaskFinishedTriageReasonInWorkerSummary(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 2, 30, 0, time.UTC)
	model := NewModel(func() time.Time { return now })

	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-3", TaskTitle: "Third", WorkerID: "worker-3", Timestamp: now.Add(-4 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskFinished, TaskID: "task-3", TaskTitle: "Third", WorkerID: "worker-3", Message: "failed", Metadata: map[string]string{"triage_reason": "review verdict returned fail", "triage_status": "failed"}, Timestamp: now.Add(-1 * time.Second)})

	state := model.UIState()
	if len(state.WorkerSummaries) != 1 {
		t.Fatalf("expected one worker summary, got %#v", state.WorkerSummaries)
	}
	if state.WorkerSummaries[0].LastEvent != "failed | review verdict returned fail" {
		t.Fatalf("expected worker last event to include triage reason, got %#v", state.WorkerSummaries[0])
	}
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

func TestModelRendersTaskPriorityQueueWithSortedOrdering(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 19, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })

	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-a", TaskTitle: "Alpha", WorkerID: "worker-0", QueuePos: 2, Priority: 10, Timestamp: now.Add(-5 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-b", TaskTitle: "Beta", WorkerID: "worker-1", QueuePos: 1, Priority: 3, Timestamp: now.Add(-4 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-c", TaskTitle: "Charlie", WorkerID: "worker-2", QueuePos: 3, Priority: 8, Timestamp: now.Add(-3 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-d", TaskTitle: "Delta", WorkerID: "worker-3", QueuePos: 2, Priority: 7, Timestamp: now.Add(-2 * time.Second)})

	state := model.UIState()
	if len(state.Queue) < 4 {
		t.Fatalf("expected 4 queue rows, got %#v", state.Queue)
	}
	if !strings.Contains(state.Queue[0], "task-b") || !strings.Contains(state.Queue[0], "q=1") || !strings.Contains(state.Queue[0], "p=3") {
		t.Fatalf("expected highest queue priority ordering in first row, got %#v", state.Queue[0])
	}
	if !strings.Contains(state.Queue[1], "task-a") || !strings.Contains(state.Queue[1], "q=2") || !strings.Contains(state.Queue[1], "p=10") {
		t.Fatalf("expected queue position ordering in second row, got %#v", state.Queue[1])
	}
	if !strings.Contains(state.Queue[2], "task-d") || !strings.Contains(state.Queue[2], "q=2") || !strings.Contains(state.Queue[2], "p=7") {
		t.Fatalf("expected queue position then priority ordering in third row, got %#v", state.Queue[2])
	}
	if !strings.Contains(state.Queue[3], "task-c") || !strings.Contains(state.Queue[3], "q=3") || !strings.Contains(state.Queue[3], "p=8") {
		t.Fatalf("expected queue tail row to be task-c, got %#v", state.Queue[3])
	}
}

func TestModelTaskDetailsPanelTracksSelectedTaskOnPanelNavigation(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 20, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })
	model.panelExpand["tasks"] = true

	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-a", TaskTitle: "Root", Message: "root", Timestamp: now.Add(-8 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-b", TaskTitle: "Child", QueuePos: 1, Priority: 4, Metadata: map[string]string{"parent_id": "task-a", "dependencies": "task-a, task-c"}, Timestamp: now.Add(-6 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-c", TaskTitle: "Sibling", QueuePos: 2, Timestamp: now.Add(-4 * time.Second)})

	rows := model.panelRows()
	selected := -1
	for i, row := range rows {
		if row.id == "task:task-b" {
			selected = i
			break
		}
	}
	if selected < 0 {
		t.Fatalf("expected task row for task-b, got %#v", rows)
	}
	model.panelCursor = selected

	state := model.UIState()
	if !strings.Contains(strings.Join(state.TaskDetails, "\n"), "task=task-b - Child") {
		t.Fatalf("expected task detail view to include selected task, got %#v", state.TaskDetails)
	}
	if !strings.Contains(strings.Join(state.TaskDetails, "\n"), "parent=task-a") {
		t.Fatalf("expected task details to include parent id, got %#v", state.TaskDetails)
	}
	if !strings.Contains(strings.Join(state.TaskDetails, "\n"), "dependencies=task-a, task-c") {
		t.Fatalf("expected task details to include normalized dependencies, got %#v", state.TaskDetails)
	}
}

func TestModelRendersTaskGraphHierarchyFromParentMetadata(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 21, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })

	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-a", TaskTitle: "Root", Timestamp: now.Add(-9 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-b", TaskTitle: "Child", Metadata: map[string]string{"parent_id": "task-a"}, Timestamp: now.Add(-8 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-c", TaskTitle: "Grandchild", Metadata: map[string]string{"parent_id": "task-b"}, Timestamp: now.Add(-7 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-d", TaskTitle: "Sibling", Metadata: map[string]string{"parent_id": "task-a"}, Timestamp: now.Add(-6 * time.Second)})

	lines := model.UIState().TaskGraph
	if len(lines) < 4 {
		t.Fatalf("expected task graph rows for hierarchy, got %#v", lines)
	}
	if !strings.HasSuffix(lines[0], "task-a - Root") {
		t.Fatalf("expected graph root row for task-a, got %#v", lines)
	}
	if !strings.HasPrefix(lines[1], "  task-b - Child") {
		t.Fatalf("expected indented child row for task-b, got %#v", lines)
	}
	if !strings.HasPrefix(lines[2], "    task-c - Grandchild") {
		t.Fatalf("expected deeper indented child row for task-c, got %#v", lines)
	}
	if !strings.HasPrefix(lines[3], "  task-d - Sibling") {
		t.Fatalf("expected sibling row for task-d, got %#v", lines)
	}
}

func TestModelRendersTaskGraphFromDependenciesMetadata(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 22, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })

	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-a", TaskTitle: "Alpha", Timestamp: now.Add(-9 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-b", TaskTitle: "Beta", Metadata: map[string]string{"dependencies": "task-a, task-a, task-a"}, Timestamp: now.Add(-8 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-c", TaskTitle: "Gamma", Metadata: map[string]string{"dependencies": "task-b"}, Timestamp: now.Add(-7 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-d", TaskTitle: "Delta", Metadata: map[string]string{"dependencies": " task-a , task-b "}, Timestamp: now.Add(-6 * time.Second)})

	lines := model.UIState().TaskGraph
	if len(lines) < 4 {
		t.Fatalf("expected at least four graph rows, got %#v", lines)
	}
	if !strings.HasSuffix(lines[0], "task-a - Alpha") {
		t.Fatalf("expected dependency graph root row for task-a, got %#v", lines)
	}
	if !strings.HasPrefix(lines[1], "  task-b - Beta") {
		t.Fatalf("expected dependency child row for task-b, got %#v", lines)
	}
	if !strings.HasPrefix(lines[2], "    task-c - Gamma") {
		t.Fatalf("expected transitive dependency row for task-c, got %#v", lines)
	}
	if !strings.HasPrefix(lines[3], "  task-d - Delta") {
		t.Fatalf("expected second dependency branch row for task-d, got %#v", lines)
	}
}

func TestParseTaskDependenciesNormalizesWhitespaceAndSorting(t *testing.T) {
	parsed := parseTaskDependencies("  task-2, task-1 ,,task-1 , task-3")
	if len(parsed) != 3 {
		t.Fatalf("expected dedupe/split results, got %#v", parsed)
	}
	if parsed[0] != "task-2" || parsed[1] != "task-1" || parsed[2] != "task-3" {
		t.Fatalf("unexpected parse result %#v", parsed)
	}

	merged := dedupeSortedDependencies(parsed, []string{"task-5", "task-4", "task-2"})
	if len(merged) != 5 {
		t.Fatalf("expected merged dependency list, got %#v", merged)
	}
	if merged[0] != "task-1" || merged[4] != "task-5" {
		t.Fatalf("expected sorted merge output, got %#v", merged)
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

func TestModelSurfacesActiveOpencodeProgressDuringHeartbeat(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 6, 30, 0, time.UTC)
	model := NewModel(func() time.Time { return now })

	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-4", TaskTitle: "Long step", WorkerID: "worker-3", Timestamp: now.Add(-70 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeRunnerCommandStarted, TaskID: "task-4", WorkerID: "worker-3", Message: "go test ./...", Timestamp: now.Add(-65 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeRunnerHeartbeat, TaskID: "task-4", WorkerID: "worker-3", Message: "alive", Metadata: map[string]string{"last_output_age": "45s"}, Timestamp: now.Add(-20 * time.Second)})

	state := model.UIState()
	if len(state.WorkerSummaries) != 1 {
		t.Fatalf("expected single worker summary, got %#v", state.WorkerSummaries)
	}
	worker := state.WorkerSummaries[0]
	if worker.LastEvent != "active: go test ./... (last output 45s)" {
		t.Fatalf("expected heartbeat to surface active opencode progress, got %#v", worker)
	}
	if len(worker.RecentTaskEvents) == 0 || worker.RecentTaskEvents[0] != "📌 active: go test ./... (last output 45s)" {
		t.Fatalf("expected recent events to include active opencode progress, got %#v", worker.RecentTaskEvents)
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

func TestModelSupportsSpacebarShortcutLiteralForToggle(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 11, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })

	model.Apply(contracts.Event{Type: contracts.EventTypeRunStarted, Metadata: map[string]string{"root_id": "yr-2y0b"}, Timestamp: now.Add(-10 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-1", TaskTitle: "First", WorkerID: "worker-0", QueuePos: 1, Timestamp: now.Add(-9 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeRunnerWarning, TaskID: "task-1", WorkerID: "worker-0", Message: "stalled", Timestamp: now.Add(-8 * time.Second)})

	model.HandleKey("down")
	model.HandleKey(" ")
	view := model.View()
	assertContains(t, view, "> [+] Workers severity=warning")

	model.HandleKey(" ")
	view = model.View()
	assertContains(t, view, "> [-] Workers severity=warning")
	assertContains(t, view, "[+] worker-0 severity=warning")
}

func TestModelPanelMarksCompletedTaskInTaskPanel(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 15, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })
	model.panelExpand["tasks"] = true

	model.Apply(contracts.Event{
		Type:      contracts.EventTypeTaskStarted,
		TaskID:    "task-1",
		TaskTitle: "First",
		WorkerID:  "worker-0",
		QueuePos:  1,
		Timestamp: now.Add(-8 * time.Second),
	})
	model.Apply(contracts.Event{
		Type:      contracts.EventTypeRunnerFinished,
		TaskID:    "task-1",
		TaskTitle: "First",
		WorkerID:  "worker-0",
		Message:   "completed",
		Timestamp: now.Add(-6 * time.Second),
	})

	state := model.UIState()
	line, ok := panelLineForTask(state.PanelLines, "task-1")
	if !ok {
		t.Fatalf("expected panel line for task-1, got %#v", state.PanelLines)
	}
	if !strings.Contains(line.Label, "✅") || !strings.HasPrefix(line.Label, "✅ ") {
		t.Fatalf("expected completed marker in panel label, got %#v", line.Label)
	}
}

func TestModelPanelDoesNotMarkInProgressTaskAsCompleted(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 16, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })
	model.panelExpand["tasks"] = true

	model.Apply(contracts.Event{
		Type:      contracts.EventTypeTaskStarted,
		TaskID:    "task-1",
		TaskTitle: "In Progress",
		WorkerID:  "worker-0",
		QueuePos:  1,
		Timestamp: now.Add(-5 * time.Second),
	})

	state := model.UIState()
	line, ok := panelLineForTask(state.PanelLines, "task-1")
	if !ok {
		t.Fatalf("expected panel line for task-1, got %#v", state.PanelLines)
	}
	if strings.Contains(line.Label, "✅") {
		t.Fatalf("did not expect completed marker on in-progress task, got %#v", line.Label)
	}
}

func TestModelUIPanelLinesExposeCompletedTaskState(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 17, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })
	model.panelRowsDirty = true
	model.panelExpand["tasks"] = true

	model.Apply(contracts.Event{
		Type:      contracts.EventTypeTaskStarted,
		TaskID:    "task-1",
		TaskTitle: "Completed",
		QueuePos:  1,
		Timestamp: now.Add(-8 * time.Second),
	})
	model.Apply(contracts.Event{
		Type:      contracts.EventTypeTaskFinished,
		TaskID:    "task-1",
		TaskTitle: "Completed",
		Message:   "done",
		Timestamp: now.Add(-6 * time.Second),
	})
	model.Apply(contracts.Event{
		Type:      contracts.EventTypeTaskStarted,
		TaskID:    "task-2",
		TaskTitle: "In Progress",
		QueuePos:  2,
		Timestamp: now.Add(-4 * time.Second),
	})

	state := model.UIState()
	completed, ok := panelLineForTask(state.PanelLines, "task-1")
	if !ok {
		t.Fatalf("expected panel line for task-1, got %#v", state.PanelLines)
	}
	if !completed.Completed {
		t.Fatalf("expected completed task line to be marked completed, got %#v", completed)
	}
	inProgress, ok := panelLineForTask(state.PanelLines, "task-2")
	if !ok {
		t.Fatalf("expected panel line for task-2, got %#v", state.PanelLines)
	}
	if inProgress.Completed {
		t.Fatalf("did not expect in-progress task line to be marked completed, got %#v", inProgress)
	}
}

func TestModelCompletedTaskMarkerPersistsWhilePanelScrolls(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 18, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })
	model.SetViewportHeight(5)
	model.panelExpand["tasks"] = true

	for i := 1; i <= 12; i++ {
		model.Apply(contracts.Event{
			Type:      contracts.EventTypeTaskStarted,
			TaskID:    fmt.Sprintf("task-%02d", i),
			TaskTitle: fmt.Sprintf("Task %02d", i),
			QueuePos:  i,
			Timestamp: now.Add(time.Duration(-i) * time.Second),
		})
	}
	model.Apply(contracts.Event{
		Type:      contracts.EventTypeTaskFinished,
		TaskID:    "task-07",
		TaskTitle: "Task 07",
		Message:   "completed",
		Timestamp: now.Add(-1 * time.Second),
	})

	visibleRows := model.panelRows()
	completedRow, ok := panelRowForTask(visibleRows, "task-07")
	if !ok {
		t.Fatalf("expected task-07 in panel rows, got %#v", visibleRows)
	}
	if !completedRow.completed {
		t.Fatalf("expected completed marker state for task-07 before scrolling, got %#v", completedRow)
	}

	for i := 0; i < 6; i++ {
		model.HandleKey("down")
		model.HandleKey("down")
		model.HandleKey("up")

		completedRow, ok = panelRowForTask(model.panelRows(), "task-07")
		if !ok {
			t.Fatalf("expected task-07 in panel rows after scroll step %d, got %#v", i, model.panelRows())
		}
		if !completedRow.completed {
			t.Fatalf("expected completed marker state for task-07 after scroll step %d, got %#v", i, completedRow)
		}

		state := model.UIState()
		if visibleLine, visible := panelLineForTask(state.PanelLines, "task-07"); visible && !visibleLine.Completed {
			t.Fatalf("expected visible task-07 line to remain completed during scroll step %d, got %#v", i, visibleLine)
		}
	}
}

func TestModelInvalidKeysDoNotCorruptPanelNavigationState(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 12, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })

	model.Apply(contracts.Event{Type: contracts.EventTypeRunStarted, Metadata: map[string]string{"root_id": "yr-2y0b"}, Timestamp: now.Add(-10 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-1", TaskTitle: "First", WorkerID: "worker-0", QueuePos: 1, Timestamp: now.Add(-9 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeRunnerWarning, TaskID: "task-1", WorkerID: "worker-0", Message: "stalled", Timestamp: now.Add(-8 * time.Second)})

	model.HandleKey("down")
	model.HandleKey("down")

	beforeCursor := model.panelCursor
	beforeRowsDirty := model.panelRowsDirty
	beforeExpand := clonePanelExpand(model.panelExpand)

	for _, key := range []string{"", "tab", "foo", "ctrl+x", "escape", "invalid"} {
		model.HandleKey(key)
	}

	if model.panelCursor != beforeCursor {
		t.Fatalf("expected panel cursor to remain %d, got %d", beforeCursor, model.panelCursor)
	}
	if model.panelRowsDirty != beforeRowsDirty {
		t.Fatalf("expected panelRowsDirty to remain %t, got %t", beforeRowsDirty, model.panelRowsDirty)
	}
	if !panelExpandEquals(beforeExpand, model.panelExpand) {
		t.Fatalf("expected panel expansion state to be unchanged")
	}
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

func TestModelBuildsStructuredUIStateWithWorkerActivity(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 13, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })

	model.Apply(contracts.Event{Type: contracts.EventTypeRunStarted, Metadata: map[string]string{"root_id": "yr-s0go", "concurrency": "2"}, Timestamp: now.Add(-10 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-1", TaskTitle: "First", WorkerID: "worker-0", QueuePos: 1, Timestamp: now.Add(-9 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeRunnerCommandStarted, TaskID: "task-1", WorkerID: "worker-0", Message: "go test ./...", Timestamp: now.Add(-8 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeRunnerWarning, TaskID: "task-1", WorkerID: "worker-0", Message: "stalled", Timestamp: now.Add(-7 * time.Second)})

	state := model.UIState()
	if state.CurrentTask != "task-1 - First" {
		t.Fatalf("expected current task in ui state, got %#v", state)
	}
	if len(state.PanelLines) == 0 {
		t.Fatalf("expected panel lines in ui state")
	}
	if len(state.WorkerSummaries) != 1 {
		t.Fatalf("expected single worker summary, got %#v", state.WorkerSummaries)
	}
	worker := state.WorkerSummaries[0]
	if worker.WorkerID != "worker-0" || worker.Task != "task-1 - First" {
		t.Fatalf("unexpected worker summary %#v", worker)
	}
	if worker.LastEvent != "stalled" {
		t.Fatalf("expected last event stalled, got %#v", worker)
	}
	if worker.LastActivityAge != "7s" {
		t.Fatalf("expected last activity age 7s, got %#v", worker)
	}
	if len(worker.RecentTaskEvents) == 0 {
		t.Fatalf("expected worker recent task events")
	}
}

func TestModelCountsDoneTasksAsCompleted(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 14, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })

	model.Apply(contracts.Event{
		Type:      contracts.EventTypeTaskStarted,
		TaskID:    "task-1",
		TaskTitle: "First",
		WorkerID:  "worker-0",
		QueuePos:  1,
		Timestamp: now.Add(-8 * time.Second),
	})
	model.Apply(contracts.Event{
		Type:      contracts.EventTypeTaskFinished,
		TaskID:    "task-1",
		TaskTitle: "First",
		Message:   "done",
		Timestamp: now.Add(-6 * time.Second),
	})
	model.Apply(contracts.Event{
		Type:      contracts.EventTypeTaskStarted,
		TaskID:    "task-2",
		TaskTitle: "Second",
		WorkerID:  "worker-1",
		QueuePos:  2,
		Timestamp: now.Add(-4 * time.Second),
	})

	metrics := model.UIState().StatusMetrics
	if metrics.completed != 1 {
		t.Fatalf("expected completed=1, got %d", metrics.completed)
	}
	if metrics.total != 2 {
		t.Fatalf("expected total=2, got %d", metrics.total)
	}
	if metrics.inProgress != 1 {
		t.Fatalf("expected in_progress=1, got %d", metrics.inProgress)
	}
}

func TestModelProvidesExecutorDashboardAndQueueFiltering(t *testing.T) {
	now := time.Date(2026, 2, 10, 12, 30, 0, 0, time.UTC)
	model := NewModel(func() time.Time { return now })

	model.Apply(contracts.Event{
		Type:      contracts.EventTypeRunStarted,
		Metadata:  map[string]string{"root_id": "yr-root", "concurrency": "3"},
		Timestamp: now.Add(-20 * time.Second),
	})

	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-done", TaskTitle: "Done", WorkerID: "worker-0", QueuePos: 1, Timestamp: now.Add(-19 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskFinished, TaskID: "task-done", TaskTitle: "Done", WorkerID: "worker-0", Message: "done", Timestamp: now.Add(-18 * time.Second)})

	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-failed", TaskTitle: "Failed", WorkerID: "worker-1", QueuePos: 2, Timestamp: now.Add(-17 * time.Second)})
	model.Apply(contracts.Event{Type: contracts.EventTypeTaskFinished, TaskID: "task-failed", TaskTitle: "Failed", WorkerID: "worker-1", Message: "failed", Timestamp: now.Add(-16 * time.Second)})

	model.Apply(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-active", TaskTitle: "Active", WorkerID: "worker-2", QueuePos: 3, Timestamp: now.Add(-15 * time.Second)})

	state := model.UIState()
	if len(state.ExecutorDashboard) == 0 {
		t.Fatalf("expected executor dashboard lines, got %#v", state.ExecutorDashboard)
	}
	if !strings.Contains(strings.Join(state.ExecutorDashboard, "\n"), "workers_total=3") {
		t.Fatalf("expected executor dashboard to include worker totals, got %#v", state.ExecutorDashboard)
	}
	if !strings.Contains(strings.Join(state.ExecutorDashboard, "\n"), "queue_filter=all") {
		t.Fatalf("expected executor dashboard to include queue filter state, got %#v", state.ExecutorDashboard)
	}
	if len(state.Queue) < 3 {
		t.Fatalf("expected unfiltered queue rows for all tasks, got %#v", state.Queue)
	}

	model.CycleQueueFilter()
	filtered := model.UIState()
	if !strings.Contains(strings.Join(filtered.ExecutorDashboard, "\n"), "queue_filter=active") {
		t.Fatalf("expected active queue filter in dashboard, got %#v", filtered.ExecutorDashboard)
	}
	if len(filtered.Queue) != 1 {
		t.Fatalf("expected active queue filter to keep one queued task, got %#v", filtered.Queue)
	}
	if !strings.Contains(filtered.Queue[0], "task-active") {
		t.Fatalf("expected active queue filter to keep task-active, got %#v", filtered.Queue)
	}
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

func clonePanelExpand(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for id, expanded := range in {
		out[id] = expanded
	}
	return out
}

func panelLineForTask(state []UIPanelLine, taskID string) (UIPanelLine, bool) {
	for _, line := range state {
		if line.ID == "task:"+taskID {
			return line, true
		}
	}
	return UIPanelLine{}, false
}

func panelRowForTask(rows []panelRow, taskID string) (panelRow, bool) {
	target := taskID
	if !strings.HasPrefix(taskID, "task:") {
		target = "task:" + taskID
	}
	for _, row := range rows {
		if row.id == target {
			return row, true
		}
	}
	return panelRow{}, false
}

func panelExpandEquals(a map[string]bool, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for id, aExpanded := range a {
		if bExpanded, ok := b[id]; !ok || bExpanded != aExpanded {
			return false
		}
	}
	return true
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
