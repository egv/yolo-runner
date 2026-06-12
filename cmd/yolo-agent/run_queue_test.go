package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
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
		materialize: func(context.Context, envpreset.Preset, string) (envpreset.Workspace, error) {
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
