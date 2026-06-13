package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/envpreset"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestRunnerDaemonOnceClaimsStubHandlerAndWritesResult(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dbPath := filepath.Join(t.TempDir(), "queue.db")
	environmentsPath := writeRunnerEnvironmentFile(t, "linux")
	store, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	item, err := store.Submit(workitem.Submission{
		Kind:           workitem.KindSplit,
		Source:         "test-source",
		SourceRef:      "TASK-1",
		IdempotencyKey: "test-source/TASK-1/split",
		Preset:         "linux",
		Payload:        json.RawMessage(`{"task_id":"TASK-1"}`),
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	code := RunMain([]string{
		"runner",
		"--queue", dbPath,
		"--environments", environmentsPath,
		"--presets", "linux",
		"--runner-id", "runner-test",
		"--once",
	}, func(context.Context, runConfig) error {
		t.Fatalf("legacy run path should not be called")
		return nil
	})
	if code != 0 {
		t.Fatalf("RunMain(runner) exit code = %d, want 0", code)
	}

	store, err = workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open(after run) error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close(after run) error = %v", err)
		}
	})

	results, err := store.ListUnconsumedResults("test-source")
	if err != nil {
		t.Fatalf("ListUnconsumedResults() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("ListUnconsumedResults() len = %d, want 1", len(results))
	}
	got := results[0]
	if got.Item.ID != item.ID {
		t.Fatalf("result item ID = %q, want %q", got.Item.ID, item.ID)
	}
	if got.Item.State != "done" {
		t.Fatalf("item state = %q, want done", got.Item.State)
	}
	if got.Result.Status != workqueue.ResultStatusCompleted {
		t.Fatalf("result status = %q, want completed", got.Result.Status)
	}

	var payload struct {
		Status string `json:"status"`
		Kind   string `json:"kind"`
		ItemID string `json:"item_id"`
	}
	if err := json.Unmarshal(got.Result.Payload, &payload); err != nil {
		t.Fatalf("unmarshal result payload %s: %v", got.Result.Payload, err)
	}
	if payload.Status != "stubbed" || payload.Kind != string(workitem.KindSplit) || payload.ItemID != item.ID {
		t.Fatalf("unexpected result payload: %#v", payload)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	var pid int
	var presets string
	var capacity int
	var startedAt string
	var heartbeatAt string
	if err := db.QueryRow(`
SELECT pid, presets, capacity, started_at, heartbeat_at
FROM runners
WHERE id = ?`, "runner-test").Scan(&pid, &presets, &capacity, &startedAt, &heartbeatAt); err != nil {
		t.Fatalf("read registered runner: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("runner pid = %d, want positive", pid)
	}
	if presets != "linux" {
		t.Fatalf("runner presets = %q, want linux", presets)
	}
	if capacity != 1 {
		t.Fatalf("runner capacity = %d, want 1", capacity)
	}
	if startedAt == "" || heartbeatAt == "" {
		t.Fatalf("runner timestamps not populated: started_at=%q heartbeat_at=%q", startedAt, heartbeatAt)
	}
}

func writeRunnerEnvironmentFile(t *testing.T, presetName string) string {
	t.Helper()

	workspacePath := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "environments.yaml")
	content := "presets:\n" +
		"  " + presetName + ":\n" +
		"    workspace:\n" +
		"      strategy: path\n" +
		"      path: " + strconv.Quote(workspacePath) + "\n" +
		"    landing:\n" +
		"      type: none\n"
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write environments file: %v", err)
	}
	return configPath
}

func TestRunnerConcurrencyRespectsPresetMaxConcurrent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "queue.db")
	store, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	runners, err := openRunnerRegistry(dbPath)
	if err != nil {
		t.Fatalf("openRunnerRegistry() error = %v", err)
	}
	t.Cleanup(func() {
		if err := runners.Close(); err != nil {
			t.Errorf("Close(runners) error = %v", err)
		}
	})
	if err := runners.Register("runner-test", []string{"arc", "path"}, 2); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	submissions := []workitem.Submission{
		{
			Kind:           workitem.KindPreflight,
			Source:         "test-source",
			SourceRef:      "ARC-1",
			IdempotencyKey: "test-source/ARC-1/preflight",
			Preset:         "arc",
			Priority:       100,
			Payload:        json.RawMessage(`{"task_id":"ARC-1"}`),
		},
		{
			Kind:           workitem.KindPreflight,
			Source:         "test-source",
			SourceRef:      "ARC-2",
			IdempotencyKey: "test-source/ARC-2/preflight",
			Preset:         "arc",
			Priority:       90,
			Payload:        json.RawMessage(`{"task_id":"ARC-2"}`),
		},
		{
			Kind:           workitem.KindPreflight,
			Source:         "test-source",
			SourceRef:      "PATH-1",
			IdempotencyKey: "test-source/PATH-1/preflight",
			Preset:         "path",
			Priority:       1,
			Payload:        json.RawMessage(`{"task_id":"PATH-1"}`),
		},
	}
	for _, submission := range submissions {
		if _, err := store.Submit(submission); err != nil {
			t.Fatalf("Submit(%s) error = %v", submission.SourceRef, err)
		}
	}

	started := make(chan workitem.Item, len(submissions))
	releaseArc := make(chan struct{})
	var releaseArcOnce sync.Once
	releaseArcItems := func() {
		releaseArcOnce.Do(func() {
			close(releaseArc)
		})
	}
	defer releaseArcItems()

	var mu sync.Mutex
	activeByPreset := map[string]int{}
	maxActiveByPreset := map[string]int{}
	handler := func(ctx context.Context, item workitem.Item, _ envpreset.Workspace) (workqueue.Result, error) {
		mu.Lock()
		activeByPreset[item.Preset]++
		if activeByPreset[item.Preset] > maxActiveByPreset[item.Preset] {
			maxActiveByPreset[item.Preset] = activeByPreset[item.Preset]
		}
		mu.Unlock()
		defer func() {
			mu.Lock()
			activeByPreset[item.Preset]--
			mu.Unlock()
		}()

		started <- item
		if item.Preset == "arc" {
			select {
			case <-releaseArc:
			case <-ctx.Done():
				return workqueue.Result{}, ctx.Err()
			}
		}
		return workqueue.Result{Payload: json.RawMessage(`{"ok":true}`)}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	daemon := runnerDaemon{
		store:   store,
		runners: runners,
		events:  runnerDaemonNoopEventSink{},
		handlers: runnerKindRegistry{
			workitem.KindPreflight: handler,
		},
		environmentPresets: map[string]envpreset.Preset{
			"arc": {
				Workspace: envpreset.Workspace{Strategy: envpreset.WorkspaceStrategyArcShared},
				Limits:    envpreset.Limits{MaxConcurrent: 1},
			},
			"path": {
				Workspace: envpreset.Workspace{Strategy: envpreset.WorkspaceStrategyPath},
			},
		},
		materialize: func(context.Context, envpreset.Preset, string, bool) (envpreset.Workspace, error) {
			return envpreset.Workspace{Path: t.TempDir()}, nil
		},
		cfg: runnerDaemonCommandConfig{
			presets:           []string{"arc", "path"},
			runnerID:          "runner-test",
			capacity:          2,
			pollInterval:      10 * time.Millisecond,
			heartbeatInterval: time.Hour,
			leaseTTL:          time.Minute,
		},
	}

	runDone := make(chan error, 1)
	go func() {
		runDone <- daemon.Run(ctx)
	}()
	defer func() {
		cancel()
		select {
		case err := <-runDone:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("Run() cleanup error = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("Run() did not stop during cleanup")
		}
	}()

	first := waitRunnerStartedItem(t, started)
	if first.Preset != "arc" || first.SourceRef != "ARC-1" {
		t.Fatalf("first started item = %s/%s, want arc/ARC-1", first.Preset, first.SourceRef)
	}

	second := waitRunnerStartedItem(t, started)
	if second.Preset != "path" {
		t.Fatalf("second started item = %s/%s, want path while arc is at max_concurrent", second.Preset, second.SourceRef)
	}

	mu.Lock()
	maxArcActive := maxActiveByPreset["arc"]
	mu.Unlock()
	if maxArcActive > 1 {
		t.Fatalf("arc preset ran concurrently: max active = %d, want 1", maxArcActive)
	}

	releaseArcItems()
	third := waitRunnerStartedItem(t, started)
	if third.Preset != "arc" || third.SourceRef != "ARC-2" {
		t.Fatalf("third started item = %s/%s, want arc/ARC-2 after first arc finishes", third.Preset, third.SourceRef)
	}
}

func waitRunnerStartedItem(t *testing.T, started <-chan workitem.Item) workitem.Item {
	t.Helper()

	select {
	case item := <-started:
		return item
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runner item to start")
		return workitem.Item{}
	}
}

func TestRunnerEventsFileIncludesProcItemIDAndHeartbeatKeepsLease(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dbPath := filepath.Join(t.TempDir(), "queue.db")
	store, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	item, err := store.Submit(workitem.Submission{
		Kind:           workitem.KindSplit,
		Source:         "test-source",
		SourceRef:      "TASK-HEARTBEAT",
		IdempotencyKey: "test-source/TASK-HEARTBEAT/split",
		Preset:         "linux",
		Payload:        json.RawMessage(`{"task_id":"TASK-HEARTBEAT"}`),
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	reaper, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open(reaper) error = %v", err)
	}
	t.Cleanup(func() {
		if err := reaper.Close(); err != nil {
			t.Errorf("Close(reaper) error = %v", err)
		}
	})

	runners, err := openRunnerRegistry(dbPath)
	if err != nil {
		t.Fatalf("openRunnerRegistry() error = %v", err)
	}
	t.Cleanup(func() {
		if err := runners.Close(); err != nil {
			t.Errorf("Close(runners) error = %v", err)
		}
	})
	if err := runners.Register("runner-heartbeat", []string{"linux"}, 1); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	leaseDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open lease db: %v", err)
	}
	leaseDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := leaseDB.Close(); err != nil {
			t.Errorf("Close(lease db) error = %v", err)
		}
	})

	daemon := runnerDaemon{
		store:   store,
		runners: runners,
		handlers: runnerKindRegistry{
			workitem.KindSplit: func(_ context.Context, got workitem.Item, _ envpreset.Workspace) (workqueue.Result, error) {
				if got.ID != item.ID {
					return workqueue.Result{}, fmt.Errorf("claimed item %q, want %q", got.ID, item.ID)
				}
				if got.LeaseExpiresAt.IsZero() {
					return workqueue.Result{}, fmt.Errorf("claimed item lease_expires_at is zero")
				}
				if _, err := waitForRunnerDaemonLeaseExtension(leaseDB, got.ID, got.LeaseExpiresAt, 2*time.Second); err != nil {
					return workqueue.Result{}, err
				}
				reaped, err := reaper.RequeueStale(got.LeaseExpiresAt.Add(time.Nanosecond))
				if err != nil {
					return workqueue.Result{}, err
				}
				if reaped != 0 {
					return workqueue.Result{}, fmt.Errorf("RequeueStale() reaped %d item(s), want 0 while heartbeat is active", reaped)
				}
				return workqueue.Result{Payload: json.RawMessage(`{"status":"ok"}`)}, nil
			},
		},
		environmentPresets: runnerDaemonTestPresets("linux"),
		materialize:        runnerDaemonNoopMaterializer,
		cfg: runnerDaemonCommandConfig{
			queuePath:         dbPath,
			presets:           []string{"linux"},
			runnerID:          "runner-heartbeat",
			once:              true,
			pollInterval:      time.Millisecond,
			heartbeatInterval: 10 * time.Millisecond,
			leaseTTL:          40 * time.Millisecond,
		},
	}

	if err := daemon.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(home, ".yolo-runner", "events", "runner-heartbeat.jsonl"))
	if err != nil {
		t.Fatalf("read runner events file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 {
		t.Fatalf("runner events file has %d line(s), want at least start and finish events: %q", len(lines), string(raw))
	}

	seen := map[string]bool{}
	for _, line := range lines {
		var event struct {
			Type   string `json:"type"`
			Proc   string `json:"proc"`
			ItemID string `json:"item_id"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("unmarshal event line %q: %v", line, err)
		}
		if event.Proc != "runner-heartbeat" {
			t.Fatalf("event proc = %q, want runner-heartbeat in line %q", event.Proc, line)
		}
		if event.ItemID != item.ID {
			t.Fatalf("event item_id = %q, want %q in line %q", event.ItemID, item.ID, line)
		}
		seen[event.Type] = true
	}
	for _, eventType := range []contracts.EventType{contracts.EventTypeRunnerStarted, contracts.EventTypeRunnerFinished} {
		if !seen[string(eventType)] {
			t.Fatalf("runner events missing %q; got %q", eventType, string(raw))
		}
	}
}

func waitForRunnerDaemonLeaseExtension(db *sql.DB, itemID string, previous time.Time, timeout time.Duration) (time.Time, error) {
	deadline := time.Now().Add(timeout)
	for {
		leaseExpiresAt, err := readRunnerDaemonLeaseExpiresAt(db, itemID)
		if err != nil {
			return time.Time{}, err
		}
		if leaseExpiresAt.After(previous) {
			return leaseExpiresAt, nil
		}
		if time.Now().After(deadline) {
			return time.Time{}, fmt.Errorf("lease_expires_at for %q stayed at %v, want after %v", itemID, leaseExpiresAt, previous)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func readRunnerDaemonLeaseExpiresAt(db *sql.DB, itemID string) (time.Time, error) {
	var raw string
	if err := db.QueryRow("SELECT lease_expires_at FROM work_items WHERE id = ?", itemID).Scan(&raw); err != nil {
		return time.Time{}, fmt.Errorf("read lease_expires_at for %q: %w", itemID, err)
	}
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, fmt.Errorf("lease_expires_at for %q is empty", itemID)
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse lease_expires_at for %q: %w", itemID, err)
	}
	return parsed, nil
}

func TestRunnerDaemonMaterializesEachPresetStrategyAndCleansUp(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "queue.db")
	store, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	presets := map[string]envpreset.Preset{
		"git": {
			Workspace: envpreset.Workspace{Strategy: envpreset.WorkspaceStrategyGitClone},
		},
		"arc": {
			Workspace: envpreset.Workspace{Strategy: envpreset.WorkspaceStrategyArcShared},
		},
		"path": {
			Workspace: envpreset.Workspace{Strategy: envpreset.WorkspaceStrategyPath},
		},
	}
	presetOrder := []string{"git", "arc", "path"}
	for _, presetName := range presetOrder {
		if _, err := store.Submit(workitem.Submission{
			Kind:           workitem.KindPreflight,
			Source:         "test-source",
			SourceRef:      "TASK-" + presetName,
			IdempotencyKey: "test-source/TASK-" + presetName + "/preflight",
			Preset:         presetName,
			Payload:        json.RawMessage(`{"task_id":"TASK"}`),
		}); err != nil {
			t.Fatalf("Submit(%s) error = %v", presetName, err)
		}
	}

	workspaceRoot := t.TempDir()
	materializedByStrategy := map[string]string{}
	cleanedByStrategy := map[string]bool{}
	handlerWorkspaceByPreset := map[string]string{}
	materializer := func(_ context.Context, preset envpreset.Preset, itemID string, _ bool) (envpreset.Workspace, error) {
		strategy := string(preset.Workspace.Strategy)
		path := filepath.Join(workspaceRoot, strategy, itemID)
		if err := os.MkdirAll(path, 0o755); err != nil {
			return envpreset.Workspace{}, err
		}
		materializedByStrategy[strategy] = path
		return envpreset.Workspace{
			Path: path,
			Cleanup: func() error {
				cleanedByStrategy[strategy] = true
				return os.RemoveAll(path)
			},
		}, nil
	}

	daemon := runnerDaemon{
		store: store,
		handlers: runnerKindRegistry{
			workitem.KindPreflight: func(_ context.Context, item workitem.Item, workspace envpreset.Workspace) (workqueue.Result, error) {
				if _, err := os.Stat(workspace.Path); err != nil {
					t.Fatalf("handler received unavailable workspace %q: %v", workspace.Path, err)
				}
				handlerWorkspaceByPreset[item.Preset] = workspace.Path
				return workqueue.Result{Payload: json.RawMessage(`{"ok":true}`)}, nil
			},
		},
		environmentPresets: presets,
		materialize:        materializer,
		cfg: runnerDaemonCommandConfig{
			runnerID:          "runner-test",
			heartbeatInterval: time.Hour,
		},
	}

	for range presetOrder {
		item, err := store.Claim("runner-test", presetOrder, time.Minute)
		if err != nil {
			t.Fatalf("Claim() error = %v", err)
		}
		if item == nil {
			t.Fatal("Claim() returned nil item")
		}
		if err := daemon.runClaimedItem(ctx, *item); err != nil {
			t.Fatalf("runClaimedItem(%s) error = %v", item.Preset, err)
		}
	}

	for _, presetName := range presetOrder {
		strategy := string(presets[presetName].Workspace.Strategy)
		path := materializedByStrategy[strategy]
		if path == "" {
			t.Fatalf("strategy %s was not materialized", strategy)
		}
		if handlerWorkspaceByPreset[presetName] != path {
			t.Fatalf("handler workspace for preset %s = %q, want %q", presetName, handlerWorkspaceByPreset[presetName], path)
		}
		if !cleanedByStrategy[strategy] {
			t.Fatalf("strategy %s cleanup was not called", strategy)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("strategy %s workspace should be cleaned up, stat error = %v", strategy, err)
		}
	}
}

func TestRunnerDaemonFailsClaimWithoutEnvironmentPresets(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "queue.db")
	store, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	if _, err := store.Submit(workitem.Submission{
		Kind:           workitem.KindPreflight,
		Source:         "test-source",
		SourceRef:      "TASK-missing-presets",
		IdempotencyKey: "test-source/TASK-missing-presets/preflight",
		Preset:         "linux",
		Payload:        json.RawMessage(`{"task_id":"TASK"}`),
	}); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	item, err := store.Claim("runner-test", []string{"linux"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if item == nil {
		t.Fatal("Claim() returned nil item")
	}

	handlerCalled := false
	daemon := runnerDaemon{
		store: store,
		handlers: runnerKindRegistry{
			workitem.KindPreflight: func(context.Context, workitem.Item, envpreset.Workspace) (workqueue.Result, error) {
				handlerCalled = true
				return workqueue.Result{Payload: json.RawMessage(`{"ok":true}`)}, nil
			},
		},
		cfg: runnerDaemonCommandConfig{
			runnerID:          "runner-test",
			heartbeatInterval: time.Hour,
		},
	}

	if err := daemon.runClaimedItem(ctx, *item); err != nil {
		t.Fatalf("runClaimedItem() error = %v", err)
	}
	if handlerCalled {
		t.Fatal("handler should not run without environment presets")
	}

	results, err := store.ListUnconsumedResults("test-source")
	if err != nil {
		t.Fatalf("ListUnconsumedResults() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("ListUnconsumedResults() len = %d, want 1", len(results))
	}
	if results[0].Result.Status != workqueue.ResultStatusFailed {
		t.Fatalf("result status = %q, want failed", results[0].Result.Status)
	}
	if !strings.Contains(string(results[0].Result.Payload), "environment presets are required") {
		t.Fatalf("result payload %s does not mention missing environment presets", results[0].Result.Payload)
	}
}

type runnerDaemonNoopEventSink struct{}

func (runnerDaemonNoopEventSink) Emit(context.Context, contracts.Event) error {
	return nil
}

func runnerDaemonTestPresets(names ...string) map[string]envpreset.Preset {
	presets := make(map[string]envpreset.Preset, len(names))
	for _, name := range names {
		presets[name] = envpreset.Preset{
			Workspace: envpreset.Workspace{Strategy: envpreset.WorkspaceStrategyPath},
		}
	}
	return presets
}

func runnerDaemonNoopMaterializer(context.Context, envpreset.Preset, string, bool) (envpreset.Workspace, error) {
	return envpreset.Workspace{}, nil
}
