package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/envpreset"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestRunQueueWithRunnerOnceCompletesTaskEndToEnd(t *testing.T) {
	repo := initSeededRepo(t)
	dbPath := filepath.Join(t.TempDir(), "queue.db")
	taskID := "TASK-queue"
	seedStore, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open(seed) error = %v", err)
	}
	if err := seedStore.Close(); err != nil {
		t.Fatalf("Close(seed) error = %v", err)
	}
	seedRunners, err := openRunnerRegistry(dbPath)
	if err != nil {
		t.Fatalf("openRunnerRegistry(seed) error = %v", err)
	}
	if err := seedRunners.Register("runner-once-test", []string{"linux"}, 1); err != nil {
		t.Fatalf("Register(seed) error = %v", err)
	}
	if err := seedRunners.Close(); err != nil {
		t.Fatalf("runner registry Close(seed) error = %v", err)
	}
	taskManager := newInMemoryTaskManager(contracts.Task{
		ID:          taskID,
		Title:       "Queued run task",
		Description: "Run through the queue producer/consumer path.",
		ParentID:    "root",
		Status:      contracts.TaskStatusOpen,
	})
	producerRunner := &fakeAgentRunner{}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runErrs := make(chan error, 1)
	go func() {
		runErrs <- runWithComponents(ctx, runConfig{
			repoRoot:         repo,
			rootID:           "root",
			profile:          "linux",
			queuePath:        dbPath,
			maxTasks:         1,
			retryBudget:      1,
			watchdogTimeout:  time.Minute,
			watchdogInterval: time.Second,
		}, taskManager, producerRunner, &fakeVCS{})
	}()

	itemID := waitForRunQueueItem(t, ctx, dbPath, runErrs)

	store, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("store Close() error = %v", err)
		}
	})
	runners, err := openRunnerRegistry(dbPath)
	if err != nil {
		t.Fatalf("openRunnerRegistry() error = %v", err)
	}
	t.Cleanup(func() {
		if err := runners.Close(); err != nil {
			t.Errorf("runner registry Close() error = %v", err)
		}
	})
	if err := runners.Register("runner-once-test", []string{"linux"}, 1); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	daemon := runnerDaemon{
		store:   store,
		runners: runners,
		events:  runnerDaemonNoopEventSink{},
		handlers: runnerKindRegistry{
			workitem.KindImplement: func(_ context.Context, item workitem.Item, _ envpreset.Workspace) (workqueue.Result, error) {
				if item.ID != itemID {
					return workqueue.Result{}, fmt.Errorf("runner claimed item ID = %q, want %q", item.ID, itemID)
				}
				payload, err := workitem.DecodeImplementPayload(item.Payload)
				if err != nil {
					return workqueue.Result{}, fmt.Errorf("decode implement payload %s: %w", item.Payload, err)
				}
				if payload.TaskID != taskID {
					return workqueue.Result{}, fmt.Errorf("payload task ID = %q, want %q", payload.TaskID, taskID)
				}
				resultPayload, err := json.Marshal(workitem.ImplementResult{
					Status:    string(contracts.RunnerResultCompleted),
					Branch:    "task/" + taskID,
					CommitSHA: "queued-sha",
				})
				if err != nil {
					return workqueue.Result{}, err
				}
				return workqueue.Result{Status: workqueue.ResultStatusCompleted, Payload: resultPayload}, nil
			},
		},
		environmentPresets: runnerDaemonTestPresets("linux"),
		materialize: func(context.Context, envpreset.Preset, string, bool) (envpreset.Workspace, error) {
			return envpreset.Workspace{Path: repo}, nil
		},
		cfg: runnerDaemonCommandConfig{
			presets:           []string{"linux"},
			runnerID:          "runner-once-test",
			once:              true,
			pollInterval:      time.Millisecond,
			heartbeatInterval: time.Hour,
			leaseTTL:          time.Minute,
		},
	}
	if err := daemon.Run(ctx); err != nil {
		t.Fatalf("runner daemon Run() error = %v", err)
	}

	select {
	case err := <-runErrs:
		if err != nil {
			t.Fatalf("runWithComponents() error = %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("runWithComponents() did not finish: %v", ctx.Err())
	}

	if got := taskManager.statusOf(taskID); got != contracts.TaskStatusClosed {
		t.Fatalf("task status = %q, want %q", got, contracts.TaskStatusClosed)
	}
	if len(producerRunner.requests) != 0 {
		t.Fatalf("producer runner should not run locally when --queue is set, got %d requests", len(producerRunner.requests))
	}
}

func TestEmbeddedRunnerForRunQueueCompletesTaskEndToEnd(t *testing.T) {
	repo := initSeededRepo(t)

	// Inject an isolated workspace (fake VCS) so this wiring test runs without
	// a real git remote; the embedded path's real git-clone materialization is
	// covered end-to-end by the kill-restart test.
	originalMaterializer := embeddedQueueMaterializer
	embeddedQueueMaterializer = func(_ context.Context, _ envpreset.Preset, _ string, _ bool) (envpreset.Workspace, error) {
		return envpreset.Workspace{Path: repo, VCS: &fakeVCS{}, Cleanup: func() error { return nil }}, nil
	}
	t.Cleanup(func() { embeddedQueueMaterializer = originalMaterializer })

	dbPath := filepath.Join(t.TempDir(), "queue.db")
	taskID := "TASK-embedded-queue"
	taskManager := newInMemoryTaskManager(contracts.Task{
		ID:          taskID,
		Title:       "Embedded queued run task",
		Description: "Run through the queue path without an external runner process.",
		ParentID:    "root",
		Status:      contracts.TaskStatusOpen,
	})
	fakeAgent := &fakeAgentRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted, Artifacts: map[string]string{"commit_sha": "embedded-sha"}},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true, Artifacts: map[string]string{"review_verdict": "pass"}},
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := runWithComponents(ctx, runConfig{
		repoRoot:           repo,
		rootID:             "root",
		profile:            "linux",
		queuePath:          dbPath,
		maxTasks:           1,
		retryBudget:        1,
		watchdogTimeout:    time.Minute,
		watchdogInterval:   time.Second,
		streamOutputBuffer: 64,
	}, taskManager, fakeAgent, &fakeVCS{})
	if err != nil {
		t.Fatalf("runWithComponents() error = %v", err)
	}

	if got := taskManager.statusOf(taskID); got != contracts.TaskStatusClosed {
		t.Fatalf("task status = %q, want %q", got, contracts.TaskStatusClosed)
	}
	if len(fakeAgent.requests) != 2 {
		t.Fatalf("embedded runner requests = %d, want implement+review", len(fakeAgent.requests))
	}
	if _, err := os.Stat(filepath.Join(repo, ".yolo-runner", "scheduler-state.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scheduler-state.json exists after embedded queue run: stat err=%v", err)
	}

	store, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("store Close() error = %v", err)
		}
	})
	results, err := store.ListUnconsumedResults("yolo-agent-run")
	if err != nil {
		t.Fatalf("ListUnconsumedResults() error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("queued result should be consumed by run, got %#v", results)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	var embeddedRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM runners WHERE id LIKE 'embedded-%'`).Scan(&embeddedRows); err != nil {
		t.Fatalf("count embedded runner rows: %v", err)
	}
	if embeddedRows != 0 {
		t.Fatalf("embedded runner rows after run = %d, want 0", embeddedRows)
	}
}

func TestEmbeddedQueueRunnerSupervisedWorkersShutDownCleanly(t *testing.T) {
	repo := initSeededRepo(t)
	dbPath := filepath.Join(t.TempDir(), "queue.db")
	store, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Ensure no embedded runner rows exist after supervisor stop, including one
	// row per replica worker created by this test.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	handle, err := maybeStartEmbeddedQueueRunner(ctx, runConfig{
		repoRoot:               repo,
		queuePath:              dbPath,
		profile:                "linux",
		embeddedRunnerPool:     "shut-down",
		embeddedRunnerReplicas: 2,
	}, &fakeAgentRunner{}, nil)
	if err != nil {
		t.Fatalf("maybeStartEmbeddedQueueRunner() error = %v", err)
	}
	if handle == nil {
		t.Fatalf("embedded runner handle is nil")
	}
	t.Cleanup(func() {
		_ = handle.Stop()
	})

	if got := waitForEmbeddedRunnerIDCount(t, dbPath, 2, 1*time.Second); got != 2 {
		t.Fatalf("running embedded runner count = %d, want 2", got)
	}
	if err := handle.Stop(); err != nil {
		t.Fatalf("handle.Stop() error = %v", err)
	}
	if got := waitForEmbeddedRunnerIDCount(t, dbPath, 0, 1*time.Second); got != 0 {
		t.Fatalf("embedded runner count after stop = %d, want 0", got)
	}
}

func TestEmbeddedQueueRunnerDuplicatePoolIsolation(t *testing.T) {
	repo := initSeededRepo(t)
	dbPath := filepath.Join(t.TempDir(), "queue.db")
	store, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	poolAHandle, err := maybeStartEmbeddedQueueRunner(ctx, runConfig{
		repoRoot:               repo,
		queuePath:              dbPath,
		profile:                "linux",
		embeddedRunnerPool:     "pool-a",
		embeddedRunnerReplicas: 1,
	}, &fakeAgentRunner{}, nil)
	if err != nil {
		t.Fatalf("maybeStartEmbeddedQueueRunner(pool-a) error = %v", err)
	}
	if poolAHandle == nil {
		t.Fatalf("embedded runner handle (pool-a) is nil")
	}
	t.Cleanup(func() {
		_ = poolAHandle.Stop()
	})

	poolBHandle, err := maybeStartEmbeddedQueueRunner(ctx, runConfig{
		repoRoot:               repo,
		queuePath:              dbPath,
		profile:                "linux",
		embeddedRunnerPool:     "pool-b",
		embeddedRunnerReplicas: 1,
	}, &fakeAgentRunner{}, nil)
	if err != nil {
		t.Fatalf("maybeStartEmbeddedQueueRunner(pool-b) error = %v", err)
	}
	if poolBHandle == nil {
		t.Fatalf("embedded runner handle (pool-b) is nil")
	}
	t.Cleanup(func() {
		_ = poolBHandle.Stop()
	})

	if got := waitForEmbeddedRunnerIDCount(t, dbPath, 2, 1*time.Second); got != 2 {
		t.Fatalf("running embedded runner count = %d, want 2", got)
	}

	var sawPoolA uint32
	var sawPoolB uint32
	for {
		ids, err := listEmbeddedRunnerIDs(t, dbPath)
		if err != nil {
			t.Fatalf("list embedded runner ids: %v", err)
		}
		for _, id := range ids {
			pool, ok := embeddedQueueRunnerPoolFromID(id)
			if !ok {
				t.Fatalf("runner id %q does not follow expected embedded format", id)
			}
			switch pool {
			case "pool-a":
				atomic.AddUint32(&sawPoolA, 1)
			case "pool-b":
				atomic.AddUint32(&sawPoolB, 1)
			}
		}
		if atomic.LoadUint32(&sawPoolA) == 1 && atomic.LoadUint32(&sawPoolB) == 1 {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("timed out waiting for pool isolation: saw pool-a=%d pool-b=%d", sawPoolA, sawPoolB)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := poolAHandle.Stop(); err != nil {
		t.Fatalf("pool-a handle.Stop() error = %v", err)
	}
	if err := poolBHandle.Stop(); err != nil {
		t.Fatalf("pool-b handle.Stop() error = %v", err)
	}
	if got := waitForEmbeddedRunnerIDCount(t, dbPath, 0, 1*time.Second); got != 0 {
		t.Fatalf("embedded runner count after stop = %d, want 0", got)
	}
}

func TestQueueHasLiveRunnerForPresetInPoolIgnoresNonEmbeddedRunnerIDs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "queue.db")
	store, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	runners, err := openRunnerRegistry(dbPath)
	if err != nil {
		t.Fatalf("openRunnerRegistry() error = %v", err)
	}
	t.Cleanup(func() {
		if err := runners.Close(); err != nil {
			t.Errorf("runner registry Close() error = %v", err)
		}
	})

	now := time.Now().UTC()
	heartbeatAt := now.Format(time.RFC3339Nano)
	legacyID := "legacy-runner"
	if _, err := runners.db.Exec(`
INSERT INTO runners (id, pid, presets, capacity, started_at, heartbeat_at)
VALUES (?, ?, ?, ?, ?, ?)`, legacyID, os.Getpid(), "linux", 1, heartbeatAt, heartbeatAt); err != nil {
		t.Fatalf("insert legacy runner row = %v", err)
	}

	live, err := queueHasLiveRunnerForPresetInPool(dbPath, "linux", "pool-a", now)
	if err != nil {
		t.Fatalf("queueHasLiveRunnerForPresetInPool(pool-a) error = %v", err)
	}
	if live {
		t.Fatalf("expected legacy non-embedded runner to be ignored for pool filter, got live=true")
	}

	live, err = queueHasLiveRunnerForPresetInPool(dbPath, "linux", "", now)
	if err != nil {
		t.Fatalf("queueHasLiveRunnerForPresetInPool(no-pool) error = %v", err)
	}
	if !live {
		t.Fatalf("expected legacy non-embedded runner to be considered when no pool is set")
	}

	embeddedID := embeddedQueueRunnerID("pool-a", "linux", 0)
	if _, err := runners.db.Exec(`
INSERT INTO runners (id, pid, presets, capacity, started_at, heartbeat_at)
VALUES (?, ?, ?, ?, ?, ?)`, embeddedID, os.Getpid(), "linux", 1, heartbeatAt, heartbeatAt); err != nil {
		t.Fatalf("insert embedded runner row = %v", err)
	}

	live, err = queueHasLiveRunnerForPresetInPool(dbPath, "linux", "pool-a", now)
	if err != nil {
		t.Fatalf("queueHasLiveRunnerForPresetInPool(pool-a, with embedded row) error = %v", err)
	}
	if !live {
		t.Fatalf("expected embedded runner with matching pool to be considered live")
	}

	live, err = queueHasLiveRunnerForPresetInPool(dbPath, "linux", "pool-b", now)
	if err != nil {
		t.Fatalf("queueHasLiveRunnerForPresetInPool(pool-b) error = %v", err)
	}
	if live {
		t.Fatalf("expected embedded runner with non-matching pool to be ignored")
	}
}

func waitForRunQueueItem(t *testing.T, ctx context.Context, dbPath string, runErrs <-chan error) string {
	t.Helper()

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case err := <-runErrs:
			t.Fatalf("run exited before queue item was submitted: %v", err)
		case <-ctx.Done():
			t.Fatalf("timed out waiting for queue item: %v", ctx.Err())
		case <-ticker.C:
		}

		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			continue
		}
		var itemID string
		queryErr := db.QueryRow(`
SELECT id
FROM work_items
WHERE kind = ?
ORDER BY created_at ASC
LIMIT 1`, string(workitem.KindImplement)).Scan(&itemID)
		closeErr := db.Close()
		if queryErr == nil {
			return itemID
		}
		if closeErr != nil {
			t.Fatalf("close queue db while waiting for item: %v", closeErr)
		}
	}
}

func waitForEmbeddedRunnerIDCount(t *testing.T, dbPath string, want int, timeout time.Duration) int {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		count, err := listEmbeddedRunnerCount(dbPath)
		if err != nil {
			t.Fatalf("count embedded runners: %v", err)
		}
		if count == want {
			return count
		}
		if time.Now().After(deadline) {
			return count
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func listEmbeddedRunnerCount(dbPath string) (int, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM runners WHERE id LIKE 'embedded-%'`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func listEmbeddedRunnerIDs(t *testing.T, dbPath string) ([]string, error) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id FROM runners WHERE id LIKE 'embedded-%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}
