package monitor

import (
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

func assertContains(t *testing.T, text string, expected string) {
	t.Helper()
	if !contains(text, expected) {
		t.Fatalf("expected %q in %q", expected, text)
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
