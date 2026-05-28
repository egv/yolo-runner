package startrek

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

const defaultStorageReadyLabel = "yolo-agent-ready"
const startrekSubtaskLabel = "agent:subtask"

// StorageBackend adapts Startrek issues to the storage-only contracts.StorageBackend API.
type StorageBackend struct {
	client        *Client
	readyLabel    string
	searchPerPage int
}

var _ contracts.StorageBackend = (*StorageBackend)(nil)

func NewStorageBackend(cfg Config) (*StorageBackend, error) {
	client, err := NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &StorageBackend{client: client}, nil
}

func (b *StorageBackend) GetTaskTree(ctx context.Context, queueKey string) (*contracts.TaskTree, error) {
	if b == nil || b.client == nil {
		return nil, errors.New("startrek storage backend is not initialized")
	}

	queueKey = strings.TrimSpace(queueKey)
	if queueKey == "" {
		return nil, errors.New("startrek queue key is required")
	}

	root := contracts.Task{
		ID:     queueKey,
		Title:  queueKey,
		Status: contracts.TaskStatusOpen,
	}
	tasks := map[string]contracts.Task{
		root.ID: root,
	}
	relations := make([]contracts.TaskRelation, 0)
	seenRelations := map[string]struct{}{}

	page := defaultIssueSearchPage
	perPage := b.searchPerPage
	if perPage <= 0 {
		perPage = defaultIssueSearchPerPage
	}

	issues := make([]Issue, 0)
	for {
		result, err := b.client.SearchIssues(ctx, IssueSearchOptions{
			QueueKey:   queueKey,
			ReadyLabel: b.effectiveReadyLabel(),
			Page:       page,
			PerPage:    perPage,
		})
		if err != nil {
			return nil, fmt.Errorf("search startrek queue %q: %w", queueKey, err)
		}

		issues = append(issues, result.Issues...)

		if result.TotalPages <= page || result.TotalPages <= 0 {
			break
		}
		page++
	}

	issueIDs := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		if issueID := strings.TrimSpace(issue.ID); issueID != "" {
			issueIDs[issueID] = struct{}{}
		}
	}

	issuesByID := make(map[string]Issue, len(issues))
	for _, issue := range issues {
		task := MapIssueToTask(issue, nil, TaskMappingOptions{
			QueueKey: queueKey,
			RootID:   root.ID,
		})
		task.ID = strings.TrimSpace(task.ID)
		if task.ID == "" {
			continue
		}

		task.ParentID = startrekTreeParentID(issue, root.ID, issueIDs)
		tasks[task.ID] = task
		issuesByID[task.ID] = issue
		appendUniqueStartrekRelation(&relations, seenRelations, contracts.TaskRelation{
			FromID: task.ParentID,
			ToID:   task.ID,
			Type:   contracts.RelationParent,
		})
	}

	for taskID, issue := range issuesByID {
		dependencyIDs := knownStartrekDependencyIDs(issue.DependencyIDs, tasks, taskID)
		if len(dependencyIDs) == 0 {
			continue
		}

		task := tasks[taskID]
		if task.Metadata == nil {
			task.Metadata = map[string]string{}
		}
		task.Metadata["dependencies"] = strings.Join(dependencyIDs, ",")
		tasks[taskID] = task

		for _, dependencyID := range dependencyIDs {
			appendUniqueStartrekRelation(&relations, seenRelations, contracts.TaskRelation{
				FromID: taskID,
				ToID:   dependencyID,
				Type:   contracts.RelationDependsOn,
			})
			appendUniqueStartrekRelation(&relations, seenRelations, contracts.TaskRelation{
				FromID: dependencyID,
				ToID:   taskID,
				Type:   contracts.RelationBlocks,
			})
		}
	}

	sort.Slice(relations, func(i, j int) bool {
		if relations[i].FromID != relations[j].FromID {
			return relations[i].FromID < relations[j].FromID
		}
		if relations[i].ToID != relations[j].ToID {
			return relations[i].ToID < relations[j].ToID
		}
		return relations[i].Type < relations[j].Type
	})

	return &contracts.TaskTree{
		Root:      root,
		Tasks:     tasks,
		Relations: relations,
	}, nil
}

func (b *StorageBackend) GetTask(ctx context.Context, taskID string) (*contracts.Task, error) {
	if b == nil || b.client == nil {
		return nil, errors.New("startrek storage backend is not initialized")
	}

	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, errors.New("startrek task ID is required")
	}

	issue, err := b.client.GetIssue(ctx, taskID)
	if err != nil {
		return nil, err
	}
	comments, err := b.client.GetIssueComments(ctx, taskID)
	if err != nil {
		return nil, err
	}

	queueKey := fallbackText(deriveQueueKey(issue.ID), deriveQueueKey(taskID))
	task := MapIssueToTask(issue, comments, TaskMappingOptions{
		QueueKey: queueKey,
		RootID:   queueKey,
	})
	return &task, nil
}

func (b *StorageBackend) SetTaskStatus(context.Context, string, contracts.TaskStatus) error {
	if b == nil || b.client == nil {
		return errors.New("startrek storage backend is not initialized")
	}
	return nil
}

func (b *StorageBackend) SetTaskData(context.Context, string, map[string]string) error {
	if b == nil || b.client == nil {
		return errors.New("startrek storage backend is not initialized")
	}
	return nil
}

func (b *StorageBackend) effectiveReadyLabel() string {
	if b == nil {
		return defaultStorageReadyLabel
	}
	return fallbackText(b.readyLabel, defaultStorageReadyLabel)
}

func startrekTreeParentID(issue Issue, rootID string, issueIDs map[string]struct{}) string {
	if hasStartrekLabel(issue.Labels, startrekSubtaskLabel) {
		parentID := strings.TrimSpace(issue.ParentID)
		if _, ok := issueIDs[parentID]; ok {
			return parentID
		}
	}
	return rootID
}

func knownStartrekDependencyIDs(dependencyIDs []string, tasks map[string]contracts.Task, taskID string) []string {
	if len(dependencyIDs) == 0 {
		return nil
	}

	known := make([]string, 0, len(dependencyIDs))
	seen := map[string]struct{}{}
	for _, dependencyID := range dependencyIDs {
		dependencyID = strings.TrimSpace(dependencyID)
		if dependencyID == "" || dependencyID == taskID {
			continue
		}
		if _, ok := tasks[dependencyID]; !ok {
			continue
		}
		if _, ok := seen[dependencyID]; ok {
			continue
		}
		seen[dependencyID] = struct{}{}
		known = append(known, dependencyID)
	}
	sort.Strings(known)
	return known
}

func hasStartrekLabel(labels []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, label := range labels {
		if strings.EqualFold(strings.TrimSpace(label), want) {
			return true
		}
	}
	return false
}

func appendUniqueStartrekRelation(relations *[]contracts.TaskRelation, seen map[string]struct{}, relation contracts.TaskRelation) {
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
