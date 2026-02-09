package scheduler

import (
	"sync"
	"testing"
	"time"
)

func TestTaskLockPreventsDuplicateExecutionAcrossParallelPicks(t *testing.T) {
	lock := NewTaskLock()

	picks := []string{"task-1", "task-1", "task-2"}
	executed := map[string]int{}
	var executedMu sync.Mutex

	var wg sync.WaitGroup
	for _, taskID := range picks {
		taskID := taskID
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !lock.TryLock(taskID) {
				return
			}
			defer lock.Unlock(taskID)

			time.Sleep(5 * time.Millisecond)
			executedMu.Lock()
			executed[taskID]++
			executedMu.Unlock()
		}()
	}
	wg.Wait()

	if executed["task-1"] != 1 {
		t.Fatalf("task-1 executed %d times, want 1", executed["task-1"])
	}
	if executed["task-2"] != 1 {
		t.Fatalf("task-2 executed %d times, want 1", executed["task-2"])
	}
}

func TestLandingLockSerializesLandingFromParallelCompletions(t *testing.T) {
	landing := NewLandingLock()

	var wg sync.WaitGroup
	var mu sync.Mutex
	inFlight := 0
	maxInFlight := 0

	worker := func() {
		defer wg.Done()
		landing.Lock()
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		mu.Unlock()

		time.Sleep(10 * time.Millisecond)

		mu.Lock()
		inFlight--
		mu.Unlock()
		landing.Unlock()
	}

	wg.Add(2)
	go worker()
	go worker()
	wg.Wait()

	if maxInFlight != 1 {
		t.Fatalf("landing ran concurrently: max in-flight = %d, want 1", maxInFlight)
	}
}
