package startrek

import (
	"strings"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type TaskCycleAction string

const (
	TaskCycleWait      TaskCycleAction = "wait"
	TaskCycleSplit     TaskCycleAction = "split"
	TaskCycleImplement TaskCycleAction = "implement"
)

func TrackerWatchStartrekTaskFromTree(summary contracts.TaskSummary, tasks map[string]contracts.Task) contracts.Task {
	taskID := strings.TrimSpace(summary.ID)
	if taskID != "" {
		if task, ok := tasks[taskID]; ok {
			return task
		}
	}
	return contracts.Task{
		ID:     taskID,
		Title:  strings.TrimSpace(summary.Title),
		Status: contracts.TaskStatusOpen,
	}
}

func PlanTrackerWatchStartrekTaskCycle(queueRoot contracts.Task, task contracts.Task, preflightReady bool) TaskCycleAction {
	if !preflightReady {
		return TaskCycleWait
	}
	taskID := strings.TrimSpace(task.ID)
	if taskID == "" || strings.EqualFold(taskID, strings.TrimSpace(queueRoot.ID)) {
		return TaskCycleWait
	}
	parentID := strings.TrimSpace(task.ParentID)
	if parentID == "" || strings.EqualFold(parentID, strings.TrimSpace(queueRoot.ID)) {
		return TaskCycleSplit
	}
	return TaskCycleImplement
}
