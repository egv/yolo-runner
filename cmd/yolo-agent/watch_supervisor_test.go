package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestWatchSupervisorScalesUpFromQueueDepth(t *testing.T) {
	sourceStarter := newFakeWatchSourceStarter()
	runnerStarter := newFakeWatchRunnerStarter()
	depth := &fakeWatchQueueDepth{depth: 3}
	supervisor := newWatchSupervisor(watchSupervisorConfig{
		Watch: watchConfig{
			QueuePath: "queue.db",
			Sources: []watchSourceConfig{
				{Name: "startrek-source", Type: watchSourceStartrek, Profile: "st-dev"},
			},
			RunnerPools: []watchRunnerPoolConfig{
				{Name: "startrek-pool", Source: "startrek-source", Presets: []string{"st-dev"}, MinReplicas: 1, MaxReplicas: 3, Capacity: 1},
			},
		},
		QueueDepth:    depth,
		SourceStarter: sourceStarter,
		RunnerStarter: runnerStarter,
		TickInterval:  5 * time.Millisecond,
		IdleCooldown:  time.Hour,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runWatchSupervisorForTest(t, ctx, supervisor)

	runnerStarter.waitForActive(t, "startrek-pool", 3)
	sourceStarter.waitForActive(t, "startrek-source", 1)

	cancel()
	waitWatchSupervisorDone(t, done)
}

func TestWatchSupervisorScalesDownAfterIdleCooldown(t *testing.T) {
	runnerStarter := newFakeWatchRunnerStarter()
	depth := &fakeWatchQueueDepth{depth: 3}
	supervisor := newWatchSupervisor(watchSupervisorConfig{
		Watch: watchConfig{
			QueuePath: "queue.db",
			Sources: []watchSourceConfig{
				{Name: "arcpr-source", Type: watchSourceArcPR, Profile: "arc-dev"},
			},
			RunnerPools: []watchRunnerPoolConfig{
				{Name: "arcpr-pool", Source: "arcpr-source", Presets: []string{"arc-dev"}, MinReplicas: 1, MaxReplicas: 3, Capacity: 1},
			},
		},
		QueueDepth:    depth,
		SourceStarter: newFakeWatchSourceStarter(),
		RunnerStarter: runnerStarter,
		TickInterval:  5 * time.Millisecond,
		IdleCooldown:  25 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runWatchSupervisorForTest(t, ctx, supervisor)

	runnerStarter.waitForActive(t, "arcpr-pool", 3)
	depth.setDepth(0)
	runnerStarter.waitForActive(t, "arcpr-pool", 1)

	cancel()
	waitWatchSupervisorDone(t, done)
}

func TestWatchSupervisorCancelsSourcesAndRunnersCleanly(t *testing.T) {
	sourceStarter := newFakeWatchSourceStarter()
	runnerStarter := newFakeWatchRunnerStarter()
	supervisor := newWatchSupervisor(watchSupervisorConfig{
		Watch: watchConfig{
			QueuePath: "queue.db",
			Sources: []watchSourceConfig{
				{Name: "startrek-source", Type: watchSourceStartrek, Profile: "st-dev"},
				{Name: "arcpr-source", Type: watchSourceArcPR, Profile: "arc-dev"},
			},
			RunnerPools: []watchRunnerPoolConfig{
				{Name: "startrek-pool", Source: "startrek-source", Presets: []string{"st-dev"}, MinReplicas: 2, MaxReplicas: 2, Capacity: 1},
			},
		},
		QueueDepth:    &fakeWatchQueueDepth{},
		SourceStarter: sourceStarter,
		RunnerStarter: runnerStarter,
		TickInterval:  5 * time.Millisecond,
		IdleCooldown:  time.Hour,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := runWatchSupervisorForTest(t, ctx, supervisor)

	sourceStarter.waitForActive(t, "startrek-source", 1)
	sourceStarter.waitForActive(t, "arcpr-source", 1)
	runnerStarter.waitForActive(t, "startrek-pool", 2)

	cancel()
	waitWatchSupervisorDone(t, done)

	sourceStarter.assertStopped(t, "startrek-source", 1)
	sourceStarter.assertStopped(t, "arcpr-source", 1)
	runnerStarter.assertStopped(t, "startrek-pool", 2)
}

func runWatchSupervisorForTest(t *testing.T, ctx context.Context, supervisor *watchSupervisor) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- supervisor.Run(ctx)
	}()
	return done
}

func waitWatchSupervisorDone(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("watch supervisor Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("watch supervisor did not stop")
	}
}

type fakeWatchQueueDepth struct {
	mu    sync.Mutex
	depth int
}

func (q *fakeWatchQueueDepth) PendingDepth(context.Context, string, []string) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.depth, nil
}

func (q *fakeWatchQueueDepth) setDepth(depth int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.depth = depth
}

type fakeWatchSourceStarter struct {
	mu      sync.Mutex
	handles map[string][]*fakeWatchHandle
}

func newFakeWatchSourceStarter() *fakeWatchSourceStarter {
	return &fakeWatchSourceStarter{handles: map[string][]*fakeWatchHandle{}}
}

func (s *fakeWatchSourceStarter) StartSource(_ context.Context, source watchSourceConfig, _ string) (watchManagedProcess, error) {
	handle := newFakeWatchHandle()
	s.mu.Lock()
	s.handles[source.Name] = append(s.handles[source.Name], handle)
	s.mu.Unlock()
	return handle, nil
}

func (s *fakeWatchSourceStarter) waitForActive(t *testing.T, source string, want int) {
	t.Helper()
	waitForWatchCount(t, func() int {
		s.mu.Lock()
		defer s.mu.Unlock()
		return countActiveFakeWatchHandles(s.handles[source])
	}, want)
}

func (s *fakeWatchSourceStarter) assertStopped(t *testing.T, source string, want int) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if got := countStoppedFakeWatchHandles(s.handles[source]); got != want {
		t.Fatalf("stopped source handles for %s = %d, want %d", source, got, want)
	}
}

type fakeWatchRunnerStarter struct {
	mu      sync.Mutex
	handles map[string][]*fakeWatchHandle
}

func newFakeWatchRunnerStarter() *fakeWatchRunnerStarter {
	return &fakeWatchRunnerStarter{handles: map[string][]*fakeWatchHandle{}}
}

func (s *fakeWatchRunnerStarter) StartRunner(_ context.Context, pool watchRunnerPoolConfig, _ int) (watchManagedProcess, error) {
	handle := newFakeWatchHandle()
	s.mu.Lock()
	s.handles[pool.Name] = append(s.handles[pool.Name], handle)
	s.mu.Unlock()
	return handle, nil
}

func (s *fakeWatchRunnerStarter) waitForActive(t *testing.T, pool string, want int) {
	t.Helper()
	waitForWatchCount(t, func() int {
		s.mu.Lock()
		defer s.mu.Unlock()
		return countActiveFakeWatchHandles(s.handles[pool])
	}, want)
}

func (s *fakeWatchRunnerStarter) assertStopped(t *testing.T, pool string, want int) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if got := countStoppedFakeWatchHandles(s.handles[pool]); got != want {
		t.Fatalf("stopped runner handles for %s = %d, want %d", pool, got, want)
	}
}

type fakeWatchHandle struct {
	done chan error

	mu      sync.Mutex
	stopped bool
}

func newFakeWatchHandle() *fakeWatchHandle {
	return &fakeWatchHandle{done: make(chan error, 1)}
}

func (h *fakeWatchHandle) Stop() error {
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return nil
	}
	h.stopped = true
	h.mu.Unlock()
	h.done <- context.Canceled
	return nil
}

func (h *fakeWatchHandle) Wait() error {
	return <-h.done
}

func (h *fakeWatchHandle) isStopped() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stopped
}

func countActiveFakeWatchHandles(handles []*fakeWatchHandle) int {
	active := 0
	for _, handle := range handles {
		if !handle.isStopped() {
			active++
		}
	}
	return active
}

func countStoppedFakeWatchHandles(handles []*fakeWatchHandle) int {
	stopped := 0
	for _, handle := range handles {
		if handle.isStopped() {
			stopped++
		}
	}
	return stopped
}

func waitForWatchCount(t *testing.T, count func() int, want int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if got := count(); got == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for count %d, last count %d", want, count())
		case <-ticker.C:
		}
	}
}
