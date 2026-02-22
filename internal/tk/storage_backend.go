package tk

import (
	"context"
	"fmt"
	"strings"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

// StorageBackend adapts tk tickets to the storage-only contracts.StorageBackend API.
type StorageBackend struct {
	manager *TaskManager
}

var _ contracts.StorageBackend = (*StorageBackend)(nil)

func NewStorageBackend(runner Runner) *StorageBackend {
	return &StorageBackend{manager: NewTaskManager(runner)}
}

func (b *StorageBackend) GetTaskTree(ctx context.Context, rootID string) (*contracts.TaskTree, error) {
	if b == nil || b.manager == nil {
		return nil, fmt.Errorf("tk storage backend is not initialized")
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
		return nil, nil
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
