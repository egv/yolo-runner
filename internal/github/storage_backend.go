package github

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

// StorageBackend adapts GitHub Issues to the storage-only contracts.StorageBackend API.
type StorageBackend struct {
	manager *TaskManager
}

var _ contracts.StorageBackend = (*StorageBackend)(nil)

func NewStorageBackend(cfg Config) (*StorageBackend, error) {
	manager, err := NewTaskManager(cfg)
	if err != nil {
		return nil, err
	}
	return &StorageBackend{manager: manager}, nil
}

func (b *StorageBackend) GetTaskTree(ctx context.Context, rootID string) (*contracts.TaskTree, error) {
	if b == nil || b.manager == nil {
		return nil, fmt.Errorf("github storage backend is not initialized")
	}

	rootNumber, err := parseIssueNumber(rootID, "root task ID")
	if err != nil {
		return nil, err
	}

	rootIssue, err := b.manager.fetchIssue(ctx, rootNumber)
	if err != nil {
		return nil, err
	}
	if rootIssue == nil {
		return nil, fmt.Errorf("root task %q not found", rootID)
	}

	allIssues, err := b.manager.fetchRepositoryIssues(ctx)
	if err != nil {
		return nil, err
	}
	if !issueSliceContainsNumber(allIssues, rootNumber) {
		allIssues = append(allIssues, *rootIssue)
	}

	inScope := descendantIssuesForRoot(rootNumber, allIssues)
	if len(inScope) == 0 {
		return nil, fmt.Errorf("root task %q not found", rootID)
	}

	inScopeIDs := make(map[string]struct{}, len(inScope))
	for _, issue := range inScope {
		inScopeIDs[strconv.Itoa(issue.Number)] = struct{}{}
	}

	tasks := make(map[string]contracts.Task, len(inScope))
	relations := make([]contracts.TaskRelation, 0, len(inScope)*3)
	relationSeen := make(map[string]struct{}, len(inScope)*3)

	for _, issue := range inScope {
		task := taskFromIssuePayload(issue, inScope)
		if issue.Number == rootNumber {
			task.ParentID = ""
		} else {
			parentID := ""
			if parentNumber := parentIssueNumber(issue); parentNumber > 0 {
				candidate := strconv.Itoa(parentNumber)
				if _, ok := inScopeIDs[candidate]; ok {
					parentID = candidate
					appendUniqueTaskRelation(&relations, relationSeen, contracts.TaskRelation{
						FromID: candidate,
						ToID:   task.ID,
						Type:   contracts.RelationParent,
					})
				}
			}
			task.ParentID = parentID
		}

		if deps := strings.TrimSpace(task.Metadata["dependencies"]); deps != "" {
			for _, depID := range strings.Split(deps, ",") {
				depID = strings.TrimSpace(depID)
				if depID == "" || depID == task.ID {
					continue
				}
				if _, ok := inScopeIDs[depID]; !ok {
					continue
				}
				appendUniqueTaskRelation(&relations, relationSeen, contracts.TaskRelation{
					FromID: task.ID,
					ToID:   depID,
					Type:   contracts.RelationDependsOn,
				})
				appendUniqueTaskRelation(&relations, relationSeen, contracts.TaskRelation{
					FromID: depID,
					ToID:   task.ID,
					Type:   contracts.RelationBlocks,
				})
			}
		}

		tasks[task.ID] = task
	}

	sortTaskRelations(relations)

	rootTask, ok := tasks[rootID]
	if !ok {
		return nil, fmt.Errorf("root task %q not found", rootID)
	}

	return &contracts.TaskTree{
		Root:      rootTask,
		Tasks:     tasks,
		Relations: relations,
	}, nil
}

func (b *StorageBackend) GetTask(ctx context.Context, taskID string) (*contracts.Task, error) {
	if b == nil || b.manager == nil {
		return nil, fmt.Errorf("github storage backend is not initialized")
	}

	issueNumber, err := parseIssueNumber(taskID, "task ID")
	if err != nil {
		return nil, err
	}

	issue, err := b.manager.fetchIssue(ctx, issueNumber)
	if err != nil {
		return nil, err
	}
	if issue == nil {
		return nil, fmt.Errorf("task %q not found", taskID)
	}

	allIssues, err := b.manager.fetchRepositoryIssues(ctx)
	if err != nil {
		return nil, err
	}

	task := taskFromIssuePayload(*issue, allIssues)
	return &task, nil
}

func (b *StorageBackend) SetTaskStatus(ctx context.Context, taskID string, status contracts.TaskStatus) error {
	if b == nil || b.manager == nil {
		return fmt.Errorf("github storage backend is not initialized")
	}
	return b.manager.SetTaskStatus(ctx, taskID, status)
}

func (b *StorageBackend) SetTaskData(ctx context.Context, taskID string, data map[string]string) error {
	if b == nil || b.manager == nil {
		return fmt.Errorf("github storage backend is not initialized")
	}
	return b.manager.SetTaskData(ctx, taskID, data)
}

func taskFromIssuePayload(issue githubIssuePayload, inScope []githubIssuePayload) contracts.Task {
	id := strconv.Itoa(issue.Number)
	metadata := map[string]string{}
	if deps := dependencyIDsForIssue(issue, inScope); len(deps) > 0 {
		metadata["dependencies"] = strings.Join(deps, ",")
	}
	if len(metadata) == 0 {
		metadata = nil
	}

	parentID := ""
	if parentNumber := parentIssueNumber(issue); parentNumber > 0 {
		parentID = strconv.Itoa(parentNumber)
	}

	return contracts.Task{
		ID:          id,
		Title:       fallbackText(issue.Title, id),
		Description: issue.Body,
		Status:      taskStatusFromIssueState(issue.State),
		ParentID:    parentID,
		Metadata:    metadata,
	}
}

func descendantIssuesForRoot(rootNumber int, issues []githubIssuePayload) []githubIssuePayload {
	byParent := make(map[int][]githubIssuePayload, len(issues))
	byNumber := make(map[int]githubIssuePayload, len(issues))
	for _, issue := range issues {
		if issue.Number <= 0 {
			continue
		}
		byNumber[issue.Number] = issue
		if parentNumber := parentIssueNumber(issue); parentNumber > 0 {
			byParent[parentNumber] = append(byParent[parentNumber], issue)
		}
	}

	seen := map[int]struct{}{rootNumber: {}}
	queue := []int{rootNumber}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		children := byParent[current]
		sort.Slice(children, func(i int, j int) bool {
			return children[i].Number < children[j].Number
		})
		for _, child := range children {
			if child.Number <= 0 {
				continue
			}
			if _, ok := seen[child.Number]; ok {
				continue
			}
			seen[child.Number] = struct{}{}
			queue = append(queue, child.Number)
		}
	}

	numbers := make([]int, 0, len(seen))
	for number := range seen {
		numbers = append(numbers, number)
	}
	sort.Ints(numbers)

	inScope := make([]githubIssuePayload, 0, len(numbers))
	for _, number := range numbers {
		if issue, ok := byNumber[number]; ok {
			inScope = append(inScope, issue)
		}
	}
	return inScope
}

func issueSliceContainsNumber(issues []githubIssuePayload, issueNumber int) bool {
	for _, issue := range issues {
		if issue.Number == issueNumber {
			return true
		}
	}
	return false
}

func appendUniqueTaskRelation(relations *[]contracts.TaskRelation, seen map[string]struct{}, relation contracts.TaskRelation) {
	if relation.FromID == "" || relation.ToID == "" || relation.FromID == relation.ToID {
		return
	}
	key := string(relation.Type) + "|" + relation.FromID + "|" + relation.ToID
	if _, exists := seen[key]; exists {
		return
	}
	seen[key] = struct{}{}
	*relations = append(*relations, relation)
}

func sortTaskRelations(relations []contracts.TaskRelation) {
	sort.Slice(relations, func(i int, j int) bool {
		if relations[i].Type != relations[j].Type {
			return relations[i].Type < relations[j].Type
		}
		if relations[i].FromID != relations[j].FromID {
			return compareIssueIDs(relations[i].FromID, relations[j].FromID) < 0
		}
		if relations[i].ToID != relations[j].ToID {
			return compareIssueIDs(relations[i].ToID, relations[j].ToID) < 0
		}
		return false
	})
}

func parentIssueNumber(issue githubIssuePayload) int {
	if issue.ParentIssueID != nil && *issue.ParentIssueID > 0 {
		return *issue.ParentIssueID
	}
	if parentNumber := issueNumberFromURL(issue.ParentIssueURL); parentNumber > 0 {
		return parentNumber
	}
	return 0
}

func issueNumberFromURL(rawURL string) int {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return 0
	}

	if idx := strings.IndexAny(trimmed, "?#"); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	trimmed = strings.TrimRight(trimmed, "/")
	lastSlash := strings.LastIndexByte(trimmed, '/')
	if lastSlash < 0 || lastSlash == len(trimmed)-1 {
		return 0
	}

	number, err := strconv.Atoi(trimmed[lastSlash+1:])
	if err != nil || number <= 0 {
		return 0
	}
	return number
}
