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

		for _, issue := range result.Issues {
			task := MapIssueToTask(issue, nil, TaskMappingOptions{
				QueueKey: queueKey,
				RootID:   root.ID,
			})
			task.ID = strings.TrimSpace(task.ID)
			if task.ID == "" {
				continue
			}
			task.ParentID = root.ID
			tasks[task.ID] = task
			appendUniqueStartrekRelation(&relations, seenRelations, contracts.TaskRelation{
				FromID: root.ID,
				ToID:   task.ID,
				Type:   contracts.RelationParent,
			})
		}

		if result.TotalPages <= page || result.TotalPages <= 0 {
			break
		}
		page++
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
