package beads

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// TaskManager implements the contracts.TaskManager interface for beads_rust (br)
type TaskManager struct {
	adapter       *RustAdapter
	runner        Runner
	terminalMu    sync.RWMutex
	terminalState map[string]contracts.TaskStatus
	repoRoot      string
}

type issueDependencyRecord struct {
	IssueID     string `json:"issue_id"`
	DependsOnID string `json:"depends_on_id"`
	Type        string `json:"type"`
}

type issueRecord struct {
	ID           string                  `json:"id"`
	Title        string                  `json:"title"`
	Description  string                  `json:"description"`
	Status       string                  `json:"status"`
	IssueType    string                  `json:"issue_type"`
	Dependencies []issueDependencyRecord `json:"dependencies"`
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
	if tree, err := m.getTaskTreeFromJSONL(rootID); err == nil {
		return tree, nil
	}

	// Use the adapter's Tree method to get the full tree
	rootIssue, err := m.adapter.Tree(rootID)
	if err != nil {
		return nil, err
	}

	// Get root bead for full details
	rootBead, err := m.adapter.Show(rootID)
	if err != nil {
		rootBead = Bead{ID: rootID, Title: rootID, Status: "open"}
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
	relations = append(relations, m.buildDependencyRelations(tasks)...)

	return &contracts.TaskTree{
		Root:      tasks[rootID],
		Tasks:     tasks,
		Relations: relations,
	}, nil
}

func (m *TaskManager) getTaskTreeFromJSONL(rootID string) (*contracts.TaskTree, error) {
	issuesPath := filepath.Join(m.repoRoot, ".beads", "issues.jsonl")
	file, err := os.Open(issuesPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	issues := make(map[string]issueRecord)
	childrenByParent := make(map[string][]string)
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var issue issueRecord
		if err := json.Unmarshal([]byte(line), &issue); err != nil {
			return nil, err
		}
		issues[issue.ID] = issue
		for _, dep := range issue.Dependencies {
			if dep.Type == "parent-child" {
				childrenByParent[dep.DependsOnID] = append(childrenByParent[dep.DependsOnID], issue.ID)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	rootIssue, ok := issues[rootID]
	if !ok {
		return nil, fmt.Errorf("root task %q not found in issues.jsonl", rootID)
	}

	inScope := map[string]struct{}{rootID: {}}
	queue := []string{rootID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		children := append([]string(nil), childrenByParent[current]...)
		sort.Strings(children)
		for _, childID := range children {
			if _, ok := inScope[childID]; ok {
				continue
			}
			inScope[childID] = struct{}{}
			queue = append(queue, childID)
		}
	}

	tasks := make(map[string]contracts.Task, len(inScope))
	relations := make([]contracts.TaskRelation, 0)
	missingSet := make(map[string]struct{})
	missingByTask := make(map[string][]string)
	ids := make([]string, 0, len(inScope))
	for id := range inScope {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		issue := issues[id]
		parentID := ""
		for _, dep := range issue.Dependencies {
			if dep.Type != "parent-child" {
				continue
			}
			if _, ok := inScope[dep.DependsOnID]; ok {
				parentID = dep.DependsOnID
				break
			}
		}
		tasks[id] = contracts.Task{
			ID:          issue.ID,
			Title:       issue.Title,
			Description: issue.Description,
			Status:      contracts.TaskStatus(issue.Status),
			ParentID:    parentID,
		}
		if parentID != "" {
			relations = append(relations, contracts.TaskRelation{FromID: parentID, ToID: id, Type: contracts.RelationParent})
		}
		for _, dep := range issue.Dependencies {
			if dep.Type == "parent-child" {
				continue
			}
			dependsOnID := strings.TrimSpace(dep.DependsOnID)
			if dependsOnID == "" {
				continue
			}
			if _, ok := inScope[dependsOnID]; ok {
				relations = append(relations, contracts.TaskRelation{FromID: id, ToID: dependsOnID, Type: contracts.RelationDependsOn})
				continue
			}
			if _, ok := issues[dependsOnID]; ok {
				missingSet[dependsOnID] = struct{}{}
				missingByTask[id] = append(missingByTask[id], dependsOnID)
			}
		}
	}

	sort.SliceStable(relations, func(i, j int) bool {
		if relations[i].Type != relations[j].Type {
			return relations[i].Type < relations[j].Type
		}
		if relations[i].FromID != relations[j].FromID {
			return relations[i].FromID < relations[j].FromID
		}
		return relations[i].ToID < relations[j].ToID
	})

	missingIDs := make([]string, 0, len(missingSet))
	for id := range missingSet {
		missingIDs = append(missingIDs, id)
	}
	sort.Strings(missingIDs)
	for taskID := range missingByTask {
		sort.Strings(missingByTask[taskID])
	}

	return &contracts.TaskTree{
		Root:                      contracts.Task{ID: rootIssue.ID, Title: rootIssue.Title, Description: rootIssue.Description, Status: contracts.TaskStatus(rootIssue.Status)},
		Tasks:                     tasks,
		Relations:                 relations,
		MissingDependencyIDs:      missingIDs,
		MissingDependenciesByTask: missingByTask,
	}, nil
}

// processChildren recursively processes child issues
func (m *TaskManager) processChildren(children []Issue, parentID string, tasks map[string]contracts.Task, relations *[]contracts.TaskRelation) {
	for _, child := range children {
		if child.ID == "" {
			continue
		}

		// Get full details
		bead, err := m.adapter.Show(child.ID)
		if err != nil {
			bead = Bead{ID: child.ID, Title: child.ID, Status: child.Status}
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

func (m *TaskManager) buildDependencyRelations(tasks map[string]contracts.Task) []contracts.TaskRelation {
	if len(tasks) == 0 {
		return nil
	}
	relations := make([]contracts.TaskRelation, 0)
	seen := make(map[string]struct{})
	taskIDs := make([]string, 0, len(tasks))
	for taskID := range tasks {
		taskIDs = append(taskIDs, taskID)
	}
	sort.Strings(taskIDs)
	for _, taskID := range taskIDs {
		deps, err := m.adapter.Dependencies(taskID)
		if err != nil {
			continue
		}
		for _, dep := range deps {
			dependsOnID := strings.TrimSpace(dep.DependsOnID)
			if dependsOnID == "" {
				continue
			}
			if strings.EqualFold(dep.Type, "parent-child") {
				continue
			}
			if _, ok := tasks[dependsOnID]; !ok {
				continue
			}
			key := taskID + "->" + dependsOnID
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			relations = append(relations, contracts.TaskRelation{
				FromID: taskID,
				ToID:   dependsOnID,
				Type:   contracts.RelationDependsOn,
			})
		}
	}
	sort.SliceStable(relations, func(i, j int) bool {
		if relations[i].FromID != relations[j].FromID {
			return relations[i].FromID < relations[j].FromID
		}
		if relations[i].ToID != relations[j].ToID {
			return relations[i].ToID < relations[j].ToID
		}
		return relations[i].Type < relations[j].Type
	})
	return relations
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
