package github

import (
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

type persistedTaskState struct {
	Statuses map[string]contracts.TaskStatus `json:"statuses,omitempty"`
	Data     map[string]map[string]string    `json:"data,omitempty"`
}

type localTaskStateStore struct {
	path string
	mu   sync.Mutex
}

func newLocalTaskStateStore(path string) *localTaskStateStore {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	return &localTaskStateStore{path: path}
}

func (s *localTaskStateStore) effectiveStatus(taskID string, remoteState string) contracts.TaskStatus {
	base := taskStatusFromIssueState(remoteState)
	if s == nil || base != contracts.TaskStatusOpen {
		return base
	}
	state, err := s.load()
	if err != nil {
		return base
	}
	if override, ok := state.Statuses[strings.TrimSpace(taskID)]; ok {
		switch override {
		case contracts.TaskStatusBlocked, contracts.TaskStatusFailed, contracts.TaskStatusInProgress:
			return override
		}
	}
	return base
}

func (s *localTaskStateStore) taskData(taskID string) map[string]string {
	if s == nil {
		return nil
	}
	state, err := s.load()
	if err != nil {
		return nil
	}
	return cloneTaskData(state.Data[strings.TrimSpace(taskID)])
}

func (s *localTaskStateStore) PersistTaskStatusChange(_ context.Context, taskID string, status contracts.TaskStatus) error {
	if s == nil {
		return nil
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return fmt.Errorf("task ID is required")
	}
	state, err := s.load()
	if err != nil {
		return err
	}
	if state.Statuses == nil {
		state.Statuses = map[string]contracts.TaskStatus{}
	}
	switch status {
	case contracts.TaskStatusBlocked, contracts.TaskStatusFailed, contracts.TaskStatusInProgress:
		state.Statuses[taskID] = status
	default:
		delete(state.Statuses, taskID)
	}
	return s.save(state)
}

func (s *localTaskStateStore) PersistTaskDataChange(_ context.Context, taskID string, data map[string]string) error {
	if s == nil {
		return nil
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return fmt.Errorf("task ID is required")
	}
	state, err := s.load()
	if err != nil {
		return err
	}
	if state.Data == nil {
		state.Data = map[string]map[string]string{}
	}
	if len(data) == 0 {
		delete(state.Data, taskID)
		return s.save(state)
	}
	state.Data[taskID] = cloneTaskData(data)
	return s.save(state)
}

func (s *localTaskStateStore) load() (persistedTaskState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *localTaskStateStore) loadLocked() (persistedTaskState, error) {
	state := persistedTaskState{
		Statuses: map[string]contracts.TaskStatus{},
		Data:     map[string]map[string]string{},
	}
	if s == nil || strings.TrimSpace(s.path) == "" {
		return state, nil
	}
	payload, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return persistedTaskState{}, fmt.Errorf("read github local task state %q: %w", s.path, err)
	}
	if len(payload) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(payload, &state); err != nil {
		return persistedTaskState{}, fmt.Errorf("parse github local task state %q: %w", s.path, err)
	}
	if state.Statuses == nil {
		state.Statuses = map[string]contracts.TaskStatus{}
	}
	if state.Data == nil {
		state.Data = map[string]map[string]string{}
	}
	return state, nil
}

func (s *localTaskStateStore) save(state persistedTaskState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(state)
}

func (s *localTaskStateStore) saveLocked(state persistedTaskState) error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return nil
	}
	if state.Statuses == nil {
		state.Statuses = map[string]contracts.TaskStatus{}
	}
	if state.Data == nil {
		state.Data = map[string]map[string]string{}
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create github local task state directory for %q: %w", s.path, err)
	}
	payload, err := json.MarshalIndent(normalizePersistedTaskState(state), "", "  ")
	if err != nil {
		return fmt.Errorf("encode github local task state %q: %w", s.path, err)
	}
	if err := os.WriteFile(s.path, append(payload, '\n'), 0o644); err != nil {
		return fmt.Errorf("write github local task state %q: %w", s.path, err)
	}
	return nil
}

func normalizePersistedTaskState(state persistedTaskState) persistedTaskState {
	normalized := persistedTaskState{
		Statuses: map[string]contracts.TaskStatus{},
		Data:     map[string]map[string]string{},
	}
	keys := make([]string, 0, len(state.Statuses))
	for key := range state.Statuses {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		switch state.Statuses[key] {
		case contracts.TaskStatusBlocked, contracts.TaskStatusFailed, contracts.TaskStatusInProgress:
			normalized.Statuses[trimmed] = state.Statuses[key]
		}
	}
	dataKeys := make([]string, 0, len(state.Data))
	for key := range state.Data {
		dataKeys = append(dataKeys, key)
	}
	sort.Strings(dataKeys)
	for _, key := range dataKeys {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		if cloned := cloneTaskData(state.Data[key]); len(cloned) > 0 {
			normalized.Data[trimmed] = cloned
		}
	}
	return normalized
}

func cloneTaskData(data map[string]string) map[string]string {
	if len(data) == 0 {
		return nil
	}
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	cloned := make(map[string]string, len(keys))
	for _, key := range keys {
		trimmedKey := strings.TrimSpace(key)
		trimmedValue := strings.TrimSpace(data[key])
		if trimmedKey == "" || trimmedValue == "" {
			continue
		}
		cloned[trimmedKey] = trimmedValue
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

func sameTaskData(a map[string]string, b map[string]string) bool {
	a = cloneTaskData(a)
	b = cloneTaskData(b)
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}
