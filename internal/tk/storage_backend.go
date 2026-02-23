package tk

import (
	"context"
	"fmt"
	"strings"

	"github.com/egv/yolo-runner/internal/contracts"
)

// StorageBackend adapts tk tickets to the storage-only contracts.StorageBackend API.
type StorageBackend struct {
	manager        *TaskManager
	statePersister taskStatePersister
}

var _ contracts.StorageBackend = (*StorageBackend)(nil)

func NewStorageBackend(runner Runner) *StorageBackend {
	return NewStorageBackendWithPersister(runner, noopTaskStatePersister{})
}

func NewStorageBackendWithGitPersistence(runner Runner) *StorageBackend {
	return NewStorageBackendWithPersister(runner, NewGitStatePersister(runner))
}

func NewStorageBackendWithPersister(runner Runner, persister taskStatePersister) *StorageBackend {
	if persister == nil {
		persister = noopTaskStatePersister{}
	}
	return &StorageBackend{manager: NewTaskManager(runner), statePersister: persister}
}

func (b *StorageBackend) GetTaskTree(ctx context.Context, rootID string) (*contracts.TaskTree, error) {
	if b == nil || b.manager == nil {
		return nil, fmt.Errorf("tk storage backend is not initialized")
	}
	rootID = strings.TrimSpace(rootID)
	rootTask, err := b.manager.GetTask(ctx, rootID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(rootTask.ID) == "" {
		return nil, fmt.Errorf("root task %q not found", rootID)
	}
	return b.manager.GetTaskTree(ctx, rootID)
}

func (b *StorageBackend) GetTask(ctx context.Context, taskID string) (*contracts.Task, error) {
	if b == nil || b.manager == nil {
		return nil, fmt.Errorf("tk storage backend is not initialized")
	}

	task, err := b.manager.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(task.ID) == "" {
		return nil, fmt.Errorf("task %q not found", strings.TrimSpace(taskID))
	}
	return &task, nil
}

func (b *StorageBackend) SetTaskStatus(ctx context.Context, taskID string, status contracts.TaskStatus) error {
	if b == nil || b.manager == nil {
		return fmt.Errorf("tk storage backend is not initialized")
	}
	return b.manager.SetTaskStatus(ctx, taskID, status)
}

func (b *StorageBackend) SetTaskData(ctx context.Context, taskID string, data map[string]string) error {
	if b == nil || b.manager == nil {
		return fmt.Errorf("tk storage backend is not initialized")
	}
	return b.manager.SetTaskData(ctx, taskID, data)
}

func (b *StorageBackend) PersistTaskStatusChange(ctx context.Context, taskID string, status contracts.TaskStatus) error {
	if b == nil || b.statePersister == nil {
		return nil
	}
	return b.statePersister.PersistTaskStatusChange(ctx, taskID, status)
}

func (b *StorageBackend) PersistTaskDataChange(ctx context.Context, taskID string, data map[string]string) error {
	if b == nil || b.statePersister == nil {
		return nil
	}
	return b.statePersister.PersistTaskDataChange(ctx, taskID, data)
}
