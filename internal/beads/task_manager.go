package beads

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/runner"
)

// TaskManager implements the contracts.TaskManager interface for beads_rust (br)
type TaskManager struct {
	adapter       *RustAdapter
	runner        Runner
	terminalMu    sync.RWMutex
	terminalState map[string]contracts.TaskStatus
	repoRoot      string
}

// NewTaskManager creates a new beads_rust TaskManager
func NewTaskManager(cmdRunner Runner, repoRoot string) *TaskManager {
	return &TaskManager{
		adapter:       NewRustAdapter(cmdRunner),
		runner:        cmdRunner,
		terminalState: map[string]contracts.TaskStatus{},
		repoRoot:      repoRoot,
	}
}

// NextTasks returns the next ready tasks under the given parent
func (m *TaskManager) NextTasks(ctx context.Context, parentID string) ([]contracts.TaskSummary, error) {
	issue, err := m.adapter.Ready(parentID)
	if err != nil {
		return nil, err
	}

	// If no children, return the issue itself if it's a leaf task
	if len(issue.Children) == 0 {
		if issue.ID == "" {
			return nil, nil
		}
		if m.isTerminal(issue.ID) {
			return nil, nil
		}
		if issue.IssueType == "epic" || issue.IssueType == "molecule" {
			return nil, nil
		}

		// Get title from Show since Issue doesn't have Title field
		bead, err := m.adapter.Show(issue.ID)
		if err != nil {
			return nil, err
		}

		return []contracts.TaskSummary{{
			ID:       issue.ID,
			Title:    bead.Title,
			Priority: issue.Priority,
		}}, nil
	}

	// Filter out terminal tasks and return children
	tasks := make([]contracts.TaskSummary, 0, len(issue.Children))
	for _, child := range issue.Children {
		if m.isTerminal(child.ID) {
			continue
		}
		if child.IssueType == "epic" || child.IssueType == "molecule" {
			continue
		}

		// Get title from Show
		bead, err := m.adapter.Show(child.ID)
		if err != nil {
			continue // Skip if we can't get details
		}

		tasks = append(tasks, contracts.TaskSummary{
			ID:       child.ID,
			Title:    bead.Title,
			Priority: child.Priority,
		})
	}

	// Sort by priority
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].Priority == nil || tasks[j].Priority == nil {
			return false
		}
		return *tasks[i].Priority < *tasks[j].Priority
	})

	return tasks, nil
}

// GetTask retrieves a single task by ID
func (m *TaskManager) GetTask(ctx context.Context, taskID string) (contracts.Task, error) {
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

// GetTaskTree retrieves the full task tree starting from rootID
func (m *TaskManager) GetTaskTree(ctx context.Context, rootID string) (*contracts.TaskTree, error) {
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return nil, fmt.Errorf("parent task ID is required")
	}

	// Use the adapter's Tree method to get the full tree
	rootIssue, err := m.adapter.Tree(rootID)
	if err != nil {
		return nil, err
	}

	// Get root bead for full details
	rootBead, err := m.adapter.Show(rootID)
	if err != nil {
		rootBead = runner.Bead{ID: rootID, Title: rootID, Status: "open"}
	}

	// Collect all tasks from the tree
	tasks := make(map[string]contracts.Task)
	relations := []contracts.TaskRelation{}

	// Add root task
	tasks[rootID] = contracts.Task{
		ID:          rootBead.ID,
		Title:       rootBead.Title,
		Description: rootBead.Description,
		Status:      contracts.TaskStatus(rootBead.Status),
	}

	// Process children recursively
	if len(rootIssue.Children) > 0 {
		m.processChildren(rootIssue.Children, rootID, tasks, &relations)
	}

	return &contracts.TaskTree{
		Root:      tasks[rootID],
		Tasks:     tasks,
		Relations: relations,
	}, nil
}

// processChildren recursively processes child issues
func (m *TaskManager) processChildren(children []runner.Issue, parentID string, tasks map[string]contracts.Task, relations *[]contracts.TaskRelation) {
	for _, child := range children {
		if child.ID == "" {
			continue
		}

		// Get full details
		bead, err := m.adapter.Show(child.ID)
		if err != nil {
			bead = runner.Bead{ID: child.ID, Title: child.ID, Status: child.Status}
		}

		// Add task
		tasks[child.ID] = contracts.Task{
			ID:          bead.ID,
			Title:       bead.Title,
			Description: bead.Description,
			Status:      contracts.TaskStatus(bead.Status),
			ParentID:    parentID,
		}

		// Add parent relation
		*relations = append(*relations, contracts.TaskRelation{
			FromID: parentID,
			ToID:   child.ID,
			Type:   contracts.RelationParent,
		})

		// Process grandchildren
		if len(child.Children) > 0 {
			m.processChildren(child.Children, child.ID, tasks, relations)
		}
	}
}

// SetTaskStatus updates the status of a task
func (m *TaskManager) SetTaskStatus(ctx context.Context, taskID string, status contracts.TaskStatus) error {
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

// SetTaskData sets arbitrary data on a task
func (m *TaskManager) SetTaskData(ctx context.Context, taskID string, data map[string]string) error {
	// beads_rust stores data in notes field
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		value := data[key]
		if _, err := m.adapter.run("update", taskID, "--notes", key+"="+value); err != nil {
			return err
		}
	}
	return nil
}

// Helper methods

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
