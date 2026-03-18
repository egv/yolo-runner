package beads

import (
	"context"
	"fmt"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// StorageBackend adapts beads_rust (br) to the storage-only contracts.StorageBackend API.
type StorageBackend struct {
	manager *TaskManager
}

var _ contracts.StorageBackend = (*StorageBackend)(nil)

// NewStorageBackend creates a new beads storage backend
func NewStorageBackend(runner Runner, repoRoot string) *StorageBackend {
	return &StorageBackend{
		manager: NewTaskManager(runner, repoRoot),
	}
}

// GetTaskTree retrieves the full task tree starting from rootID
func (b *StorageBackend) GetTaskTree(ctx context.Context, rootID string) (*contracts.TaskTree, error) {
	if b == nil || b.manager == nil {
		return nil, fmt.Errorf("beads storage backend is not initialized")
	}
	rootID = strings.TrimSpace(rootID)
	return b.manager.GetTaskTree(ctx, rootID)
}

// GetTask retrieves a single task by ID
func (b *StorageBackend) GetTask(ctx context.Context, taskID string) (*contracts.Task, error) {
	if b == nil || b.manager == nil {
		return nil, fmt.Errorf("beads storage backend is not initialized")
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

// SetTaskStatus updates the status of a task
func (b *StorageBackend) SetTaskStatus(ctx context.Context, taskID string, status contracts.TaskStatus) error {
	if b == nil || b.manager == nil {
		return fmt.Errorf("beads storage backend is not initialized")
	}
	return b.manager.SetTaskStatus(ctx, taskID, status)
}

// SetTaskData sets arbitrary data on a task
func (b *StorageBackend) SetTaskData(ctx context.Context, taskID string, data map[string]string) error {
	if b == nil || b.manager == nil {
		return fmt.Errorf("beads storage backend is not initialized")
	}
	return b.manager.SetTaskData(ctx, taskID, data)
}
