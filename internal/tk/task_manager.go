package tk

import (
	"context"
	"sort"
	"strings"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

type TaskManager struct {
	adapter *Adapter
	runner  Runner
}

func NewTaskManager(runner Runner) *TaskManager {
	return &TaskManager{adapter: New(runner), runner: runner}
}

func (m *TaskManager) NextTasks(_ context.Context, parentID string) ([]contracts.TaskSummary, error) {
	tickets, err := m.adapter.queryTickets()
	if err != nil {
		return nil, err
	}
	titles := map[string]string{}
	for _, t := range tickets {
		titles[t.ID] = t.Title
	}

	ready, err := m.adapter.Ready(parentID)
	if err != nil {
		return nil, err
	}

	if len(ready.Children) == 0 {
		if ready.ID == "" {
			return nil, nil
		}
		if m.isTerminalFailed(ready.ID) {
			return nil, nil
		}
		title := titles[ready.ID]
		if title == "" {
			title = ready.ID
		}
		return []contracts.TaskSummary{{ID: ready.ID, Title: title, Priority: ready.Priority}}, nil
	}

	tasks := make([]contracts.TaskSummary, 0, len(ready.Children))
	for _, child := range ready.Children {
		if m.isTerminalFailed(child.ID) {
			continue
		}
		title := titles[child.ID]
		if title == "" {
			title = child.ID
		}
		tasks = append(tasks, contracts.TaskSummary{ID: child.ID, Title: title, Priority: child.Priority})
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].Priority == nil || tasks[j].Priority == nil {
			return false
		}
		return *tasks[i].Priority < *tasks[j].Priority
	})
	return tasks, nil
}

func (m *TaskManager) GetTask(_ context.Context, taskID string) (contracts.Task, error) {
	bead, err := m.adapter.Show(taskID)
	if err != nil {
		return contracts.Task{}, err
	}
	return contracts.Task{
		ID:          bead.ID,
		Title:       bead.Title,
		Description: bead.Description,
		Status:      contracts.TaskStatus(bead.Status),
	}, nil
}

func (m *TaskManager) SetTaskStatus(_ context.Context, taskID string, status contracts.TaskStatus) error {
	return m.adapter.UpdateStatus(taskID, string(status))
}

func (m *TaskManager) SetTaskData(_ context.Context, taskID string, data map[string]string) error {
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := m.runner.Run("tk", "add-note", taskID, key+"="+data[key]); err != nil {
			return err
		}
	}
	return nil
}

func (m *TaskManager) isTerminalFailed(taskID string) bool {
	if taskID == "" || m.runner == nil {
		return false
	}
	out, err := m.runner.Run("tk", "show", taskID)
	if err != nil {
		return false
	}
	return strings.Contains(out, "triage_status=failed") || strings.Contains(out, "terminal_state=failed")
}
