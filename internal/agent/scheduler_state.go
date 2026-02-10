package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

type schedulerStateStore struct {
	mu       sync.Mutex
	path     string
	parentID string
}

type schedulerStateFile struct {
	Parents map[string]schedulerParentState `json:"parents"`
}

type schedulerParentState struct {
	InFlight  []string `json:"in_flight,omitempty"`
	Completed []string `json:"completed,omitempty"`
	Blocked   []string `json:"blocked,omitempty"`
}

type schedulerStateSnapshot struct {
	InFlight  map[string]struct{}
	Completed map[string]struct{}
	Blocked   map[string]struct{}
}

func newSchedulerStateStore(path string, parentID string) *schedulerStateStore {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(parentID) == "" {
		return nil
	}
	return &schedulerStateStore{path: path, parentID: parentID}
}

func (l *Loop) recoverSchedulerState(ctx context.Context) error {
	if l.schedulerState == nil {
		return nil
	}

	snapshot, err := l.schedulerState.Load()
	if err != nil {
		return err
	}

	for taskID := range snapshot.Completed {
		if err := l.tasks.SetTaskStatus(ctx, taskID, contracts.TaskStatusClosed); err != nil {
			return err
		}
		delete(snapshot.Completed, taskID)
	}

	for taskID := range snapshot.Blocked {
		if err := l.tasks.SetTaskStatus(ctx, taskID, contracts.TaskStatusBlocked); err != nil {
			return err
		}
		if err := l.tasks.SetTaskData(ctx, taskID, map[string]string{"triage_status": "blocked"}); err != nil {
			return err
		}
		delete(snapshot.Blocked, taskID)
	}

	for taskID := range snapshot.InFlight {
		if _, completed := snapshot.Completed[taskID]; completed {
			continue
		}
		if _, blocked := snapshot.Blocked[taskID]; blocked {
			continue
		}
		if err := l.tasks.SetTaskStatus(ctx, taskID, contracts.TaskStatusOpen); err != nil {
			return err
		}
	}

	snapshot.InFlight = map[string]struct{}{}
	return l.schedulerState.Save(snapshot)
}

func (l *Loop) markTaskInFlight(taskID string) error {
	if l.schedulerState == nil {
		return nil
	}
	snapshot, err := l.schedulerState.Load()
	if err != nil {
		return err
	}
	snapshot.InFlight[taskID] = struct{}{}
	return l.schedulerState.Save(snapshot)
}

func (l *Loop) markTaskCompleted(taskID string) error {
	if l.schedulerState == nil {
		return nil
	}
	snapshot, err := l.schedulerState.Load()
	if err != nil {
		return err
	}
	delete(snapshot.InFlight, taskID)
	snapshot.Completed[taskID] = struct{}{}
	delete(snapshot.Blocked, taskID)
	return l.schedulerState.Save(snapshot)
}

func (l *Loop) markTaskBlocked(taskID string) error {
	if l.schedulerState == nil {
		return nil
	}
	snapshot, err := l.schedulerState.Load()
	if err != nil {
		return err
	}
	delete(snapshot.InFlight, taskID)
	snapshot.Blocked[taskID] = struct{}{}
	delete(snapshot.Completed, taskID)
	return l.schedulerState.Save(snapshot)
}

func (l *Loop) clearTaskTerminalState(taskID string) error {
	if l.schedulerState == nil {
		return nil
	}
	snapshot, err := l.schedulerState.Load()
	if err != nil {
		return err
	}
	delete(snapshot.InFlight, taskID)
	delete(snapshot.Completed, taskID)
	delete(snapshot.Blocked, taskID)
	return l.schedulerState.Save(snapshot)
}

func (l *Loop) clearTaskInFlight(taskID string) error {
	if l.schedulerState == nil {
		return nil
	}
	snapshot, err := l.schedulerState.Load()
	if err != nil {
		return err
	}
	delete(snapshot.InFlight, taskID)
	return l.schedulerState.Save(snapshot)
}

func (s *schedulerStateStore) Load() (schedulerStateSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadStateFileLocked()
	if err != nil {
		return schedulerStateSnapshot{}, err
	}
	return state.snapshotForParent(s.parentID), nil
}

func (s *schedulerStateStore) Save(snapshot schedulerStateSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadStateFileLocked()
	if err != nil {
		return err
	}
	state.Parents[s.parentID] = schedulerParentState{
		InFlight:  sortedKeys(snapshot.InFlight),
		Completed: sortedKeys(snapshot.Completed),
		Blocked:   sortedKeys(snapshot.Blocked),
	}
	return s.writeStateFileLocked(state)
}

func (f schedulerStateFile) snapshotForParent(parentID string) schedulerStateSnapshot {
	parentState, ok := f.Parents[parentID]
	if !ok {
		return schedulerStateSnapshot{
			InFlight:  map[string]struct{}{},
			Completed: map[string]struct{}{},
			Blocked:   map[string]struct{}{},
		}
	}

	return schedulerStateSnapshot{
		InFlight:  makeSet(parentState.InFlight),
		Completed: makeSet(parentState.Completed),
		Blocked:   makeSet(parentState.Blocked),
	}
}

func (s *schedulerStateStore) loadStateFileLocked() (schedulerStateFile, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return schedulerStateFile{Parents: map[string]schedulerParentState{}}, nil
		}
		return schedulerStateFile{}, err
	}

	var state schedulerStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return schedulerStateFile{}, err
	}
	if state.Parents == nil {
		state.Parents = map[string]schedulerParentState{}
	}
	return state, nil
}

func (s *schedulerStateStore) writeStateFileLocked(state schedulerStateFile) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

func makeSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		result[value] = struct{}{}
	}
	return result
}

func sortedKeys(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
