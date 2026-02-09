package scheduler

import "sync"

type TaskLock struct {
	mu     sync.Mutex
	locked map[string]struct{}
}

func NewTaskLock() *TaskLock {
	return &TaskLock{locked: make(map[string]struct{})}
}

func (l *TaskLock) TryLock(taskID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, exists := l.locked[taskID]; exists {
		return false
	}
	l.locked[taskID] = struct{}{}
	return true
}

func (l *TaskLock) Unlock(taskID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.locked, taskID)
}

type LandingLock struct {
	mu sync.Mutex
}

func NewLandingLock() *LandingLock {
	return &LandingLock{}
}

func (l *LandingLock) Lock() {
	l.mu.Lock()
}

func (l *LandingLock) Unlock() {
	l.mu.Unlock()
}
