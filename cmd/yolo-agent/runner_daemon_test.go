package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/envpreset"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestRunnerDaemonOnceClaimsStubHandlerAndWritesResult(t *testing.T) {
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
	materializer := func(_ context.Context, preset envpreset.Preset, itemID string) (envpreset.Workspace, error) {
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
