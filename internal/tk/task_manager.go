package tk

import (
	"context"
	"fmt"
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

func (m *TaskManager) GetTaskTree(_ context.Context, rootID string) (*contracts.TaskTree, error) {
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return nil, fmt.Errorf("parent task ID is required")
	}

	tickets, err := m.adapter.queryTickets()
	if err != nil {
		return nil, err
	}

	ticketsByID := make(map[string]ticket, len(tickets))
	childrenByParent := make(map[string][]string, len(tickets))
	for _, t := range tickets {
		taskID := strings.TrimSpace(t.ID)
		if taskID == "" {
			continue
		}
		t.ID = taskID
		t.Parent = strings.TrimSpace(t.Parent)
		ticketsByID[taskID] = t
		if t.Parent != "" {
			childrenByParent[t.Parent] = append(childrenByParent[t.Parent], taskID)
		}
	}

	if _, ok := ticketsByID[rootID]; !ok {
		root := contracts.Task{
			ID:     rootID,
			Title:  rootID,
			Status: contracts.TaskStatusOpen,
		}
		return &contracts.TaskTree{
			Root:  root,
			Tasks: map[string]contracts.Task{rootID: root},
		}, nil
	}

	inScope := descendantTaskIDs(rootID, childrenByParent)
	taskIDs := sortedTaskIDs(inScope)

	tasks := make(map[string]contracts.Task, len(taskIDs))
	depsByTask := make(map[string][]string, len(taskIDs))
	for _, taskID := range taskIDs {
		raw := ticketsByID[taskID]
		parentID := strings.TrimSpace(raw.Parent)
		if taskID == rootID {
			parentID = ""
		} else if _, ok := inScope[parentID]; !ok {
			parentID = rootID
		}

		deps := filterDependencies(raw.Deps, inScope, taskID)
		depsByTask[taskID] = deps

		task := contracts.Task{
			ID:          taskID,
			Title:       fallbackTaskTitle(raw.Title, taskID),
			Description: raw.Description,
			Status:      fallbackTaskStatus(raw.Status),
			ParentID:    parentID,
		}
		if len(deps) > 0 {
			task.Metadata = map[string]string{"dependencies": strings.Join(deps, ",")}
		}
		tasks[taskID] = task
	}

	root := tasks[rootID]
	relations := make([]contracts.TaskRelation, 0, len(taskIDs)*3)
	relationSeen := map[string]struct{}{}
	for _, taskID := range taskIDs {
		task := tasks[taskID]
		if taskID != rootID && task.ParentID != "" {
			appendUniqueTaskRelation(&relations, relationSeen, contracts.TaskRelation{
				FromID: task.ParentID,
				ToID:   taskID,
				Type:   contracts.RelationParent,
			})
		}
		for _, depID := range depsByTask[taskID] {
			appendUniqueTaskRelation(&relations, relationSeen, contracts.TaskRelation{
				FromID: taskID,
				ToID:   depID,
				Type:   contracts.RelationDependsOn,
			})
			appendUniqueTaskRelation(&relations, relationSeen, contracts.TaskRelation{
				FromID: depID,
				ToID:   taskID,
				Type:   contracts.RelationBlocks,
			})
		}
	}
	sortTaskRelations(relations)

	return &contracts.TaskTree{
		Root:      root,
		Tasks:     tasks,
		Relations: relations,
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

func descendantTaskIDs(rootID string, childrenByParent map[string][]string) map[string]struct{} {
	seen := map[string]struct{}{rootID: {}}
	queue := []string{rootID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		children := append([]string(nil), childrenByParent[current]...)
		sort.Strings(children)
		for _, childID := range children {
			if _, ok := seen[childID]; ok {
				continue
			}
			seen[childID] = struct{}{}
			queue = append(queue, childID)
		}
	}
	return seen
}

func sortedTaskIDs(ids map[string]struct{}) []string {
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func filterDependencies(deps []string, inScope map[string]struct{}, taskID string) []string {
	if len(deps) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	filtered := make([]string, 0, len(deps))
	for _, depID := range deps {
		depID = strings.TrimSpace(depID)
		if depID == "" || depID == taskID {
			continue
		}
		if _, ok := inScope[depID]; !ok {
			continue
		}
		if _, ok := seen[depID]; ok {
			continue
		}
		seen[depID] = struct{}{}
		filtered = append(filtered, depID)
	}
	sort.Strings(filtered)
	return filtered
}

func fallbackTaskTitle(title string, fallback string) string {
	if strings.TrimSpace(title) == "" {
		return fallback
	}
	return strings.TrimSpace(title)
}

func fallbackTaskStatus(status string) contracts.TaskStatus {
	normalized := contracts.TaskStatus(strings.TrimSpace(status))
	if normalized == "" {
		return contracts.TaskStatusOpen
	}
	return normalized
}

func appendUniqueTaskRelation(relations *[]contracts.TaskRelation, seen map[string]struct{}, relation contracts.TaskRelation) {
	if relation.FromID == "" || relation.ToID == "" || relation.FromID == relation.ToID {
		return
	}
	key := string(relation.Type) + "|" + relation.FromID + "|" + relation.ToID
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	*relations = append(*relations, relation)
}

func sortTaskRelations(relations []contracts.TaskRelation) {
	sort.Slice(relations, func(i, j int) bool {
		if relations[i].Type != relations[j].Type {
			return relations[i].Type < relations[j].Type
		}
		if relations[i].FromID != relations[j].FromID {
			return relations[i].FromID < relations[j].FromID
		}
		if relations[i].ToID != relations[j].ToID {
			return relations[i].ToID < relations[j].ToID
		}
		return false
	})
}
