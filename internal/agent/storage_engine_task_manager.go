package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

type storageEngineTaskManager struct {
	mu      sync.Mutex
	storage contracts.StorageBackend
	engine  contracts.TaskEngine
	rootID  string
	graph   *contracts.TaskGraph
}

var _ contracts.TaskManager = (*storageEngineTaskManager)(nil)
var _ taskConcurrencyCalculator = (*storageEngineTaskManager)(nil)
var _ taskCompletionChecker = (*storageEngineTaskManager)(nil)

func newStorageEngineTaskManager(storage contracts.StorageBackend, taskEngine contracts.TaskEngine, rootID string) *storageEngineTaskManager {
	return &storageEngineTaskManager{
		storage: storage,
		engine:  taskEngine,
		rootID:  strings.TrimSpace(rootID),
	}
}

func (m *storageEngineTaskManager) NextTasks(ctx context.Context, parentID string) ([]contracts.TaskSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rootID, err := m.resolveRootID(parentID)
	if err != nil {
		return nil, err
	}
	if err := m.refreshGraphLocked(ctx, rootID); err != nil {
		return nil, err
	}
	return m.engine.GetNextAvailable(m.graph), nil
}

func (m *storageEngineTaskManager) GetTask(ctx context.Context, taskID string) (contracts.Task, error) {
	task, err := m.storage.GetTask(ctx, taskID)
	if err != nil {
		return contracts.Task{}, err
	}
	if task == nil {
		return contracts.Task{}, fmt.Errorf("task %q not found", taskID)
	}
	return *task, nil
}

func (m *storageEngineTaskManager) SetTaskStatus(ctx context.Context, taskID string, status contracts.TaskStatus) error {
	if err := m.storage.SetTaskStatus(ctx, taskID, status); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.graph != nil {
		if err := m.engine.UpdateTaskStatus(m.graph, taskID, status); err != nil {
			return err
		}
	}
	return nil
}

func (m *storageEngineTaskManager) SetTaskData(ctx context.Context, taskID string, data map[string]string) error {
	return m.storage.SetTaskData(ctx, taskID, data)
}

func (m *storageEngineTaskManager) CalculateConcurrency(ctx context.Context, maxWorkers int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rootID, err := m.resolveRootID("")
	if err != nil {
		return 0, err
	}
	if err := m.refreshGraphLocked(ctx, rootID); err != nil {
		return 0, err
	}

	concurrency := m.engine.CalculateConcurrency(m.graph, contracts.ConcurrencyOptions{MaxWorkers: maxWorkers})
	if concurrency <= 0 {
		if maxWorkers > 0 {
			return maxWorkers, nil
		}
		return 1, nil
	}
	return concurrency, nil
}

func (m *storageEngineTaskManager) IsComplete(ctx context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rootID, err := m.resolveRootID("")
	if err != nil {
		return false, err
	}
	if err := m.refreshGraphLocked(ctx, rootID); err != nil {
		return false, err
	}
	return m.engine.IsComplete(m.graph), nil
}

func (m *storageEngineTaskManager) resolveRootID(parentID string) (string, error) {
	if rootID := strings.TrimSpace(parentID); rootID != "" {
		m.rootID = rootID
		return rootID, nil
	}
	if m.rootID != "" {
		return m.rootID, nil
	}
	return "", fmt.Errorf("parent task ID is required")
}

func (m *storageEngineTaskManager) refreshGraphLocked(ctx context.Context, rootID string) error {
	if m.storage == nil {
		return fmt.Errorf("storage backend is required")
	}
	if m.engine == nil {
		return fmt.Errorf("task engine is required")
	}

	tree, err := m.storage.GetTaskTree(ctx, rootID)
	if err != nil {
		return err
	}
	graph, err := m.engine.BuildGraph(tree)
	if err != nil {
		return err
	}
	m.graph = graph
	return nil
}
