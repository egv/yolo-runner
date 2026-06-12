package startrek

import (
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestPlanTrackerWatchStartrekTaskCycle(t *testing.T) {
	queueRoot := contracts.Task{ID: "VAY"}
	tests := []struct {
		name           string
		task           contracts.Task
		preflightReady bool
		want           TaskCycleAction
	}{
		{
			name:           "unready task waits",
			task:           contracts.Task{ID: "VAY-42", ParentID: "VAY"},
			preflightReady: false,
			want:           TaskCycleWait,
		},
		{
			name:           "queue root waits",
			task:           contracts.Task{ID: "VAY"},
			preflightReady: true,
			want:           TaskCycleWait,
		},
		{
			name:           "ready top level task splits",
			task:           contracts.Task{ID: "VAY-42", ParentID: "VAY"},
			preflightReady: true,
			want:           TaskCycleSplit,
		},
		{
			name:           "ready split leaf implements",
			task:           contracts.Task{ID: "VAY-43", ParentID: "VAY-42"},
			preflightReady: true,
			want:           TaskCycleImplement,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PlanTrackerWatchStartrekTaskCycle(queueRoot, tt.task, tt.preflightReady); got != tt.want {
				t.Fatalf("PlanTrackerWatchStartrekTaskCycle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTrackerWatchStartrekTaskFromTree(t *testing.T) {
	tasks := map[string]contracts.Task{
		"VAY-42": {
			ID:       "VAY-42",
			Title:    "Stored task",
			ParentID: "VAY",
			Status:   contracts.TaskStatusInProgress,
		},
	}

	if got := TrackerWatchStartrekTaskFromTree(contracts.TaskSummary{ID: " VAY-42 ", Title: "Summary title"}, tasks); got.Title != "Stored task" || got.Status != contracts.TaskStatusInProgress {
		t.Fatalf("expected stored task, got %#v", got)
	}

	got := TrackerWatchStartrekTaskFromTree(contracts.TaskSummary{ID: " VAY-99 ", Title: " Summary title "}, tasks)
	if got.ID != "VAY-99" || got.Title != "Summary title" || got.Status != contracts.TaskStatusOpen {
		t.Fatalf("unexpected fallback task: %#v", got)
	}
}
