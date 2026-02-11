package tk

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

type TaskManager struct {
	adapter       *Adapter
	runner        Runner
	terminalMu    sync.RWMutex
	terminalState map[string]contracts.TaskStatus
}

func NewTaskManager(runner Runner) *TaskManager {
	return &TaskManager{
		adapter:       New(runner),
		runner:        runner,
		terminalState: map[string]contracts.TaskStatus{},
	}
}

func (m *TaskManager) NextTasks(_ context.Context, parentID string) ([]contracts.TaskSummary, error) {
	tickets, err := m.adapter.queryTickets()
	if err != nil {
		return nil, err
	}
	ticketsByID := map[string]ticket{}
	titles := map[string]string{}
	for _, t := range tickets {
		titles[t.ID] = t.Title
		ticketsByID[t.ID] = t
	}

	ready, err := m.adapter.Ready(parentID)
	if err != nil {
		return nil, err
	}

	if len(ready.Children) == 0 {
		if ready.ID == "" {
			return nil, nil
		}
		if m.isTerminal(ready.ID) {
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
		if m.isTerminal(child.ID) {
			continue
		}
		if !dependenciesSatisfied(ticketsByID[child.ID], ticketsByID) {
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
	metadata := map[string]string{}
	if deps, depsErr := m.dependenciesForTask(taskID); depsErr == nil && len(deps) > 0 {
		metadata["dependencies"] = strings.Join(deps, ",")
	}
	if len(metadata) == 0 {
		metadata = nil
	}
	return contracts.Task{
		ID:          bead.ID,
		Title:       bead.Title,
		Description: bead.Description,
		Status:      contracts.TaskStatus(bead.Status),
		Metadata:    metadata,
	}, nil
}

func (m *TaskManager) SetTaskStatus(_ context.Context, taskID string, status contracts.TaskStatus) error {
	if err := m.adapter.UpdateStatus(taskID, string(status)); err != nil {
		return err
	}
	switch status {
	case contracts.TaskStatusFailed, contracts.TaskStatusBlocked:
		m.markTerminal(taskID, status)
	default:
		m.clearTerminal(taskID)
	}
	return nil
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

func (m *TaskManager) isTerminal(taskID string) bool {
	if taskID == "" {
		return false
	}
	m.terminalMu.RLock()
	defer m.terminalMu.RUnlock()
	state, ok := m.terminalState[taskID]
	if !ok {
		return false
	}
	return state == contracts.TaskStatusFailed || state == contracts.TaskStatusBlocked
}

func (m *TaskManager) markTerminal(taskID string, state contracts.TaskStatus) {
	if taskID == "" {
		return
	}
	m.terminalMu.Lock()
	defer m.terminalMu.Unlock()
	m.terminalState[taskID] = state
}

func (m *TaskManager) clearTerminal(taskID string) {
	if taskID == "" {
		return
	}
	m.terminalMu.Lock()
	defer m.terminalMu.Unlock()
	delete(m.terminalState, taskID)
}

func (m *TaskManager) dependenciesForTask(taskID string) ([]string, error) {
	tickets, err := m.adapter.queryTickets()
	if err != nil {
		return nil, err
	}
	for _, t := range tickets {
		if t.ID == taskID {
			return append([]string(nil), t.Deps...), nil
		}
	}
	return nil, nil
}

func dependenciesSatisfied(task ticket, ticketsByID map[string]ticket) bool {
	if len(task.Deps) == 0 {
		return true
	}
	for _, depID := range task.Deps {
		dep, ok := ticketsByID[depID]
		if !ok {
			continue
		}
		if dep.Status != "closed" {
			return false
		}
	}
	return true
}
