package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/envpreset"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

const (
	killRestartHelperEnv        = "YOLO_KILL_RESTART_HELPER"
	killRestartQueueEnv         = "YOLO_KILL_RESTART_QUEUE"
	killRestartEnvironmentsEnv  = "YOLO_KILL_RESTART_ENVIRONMENTS"
	killRestartHomeEnv          = "YOLO_KILL_RESTART_HOME"
	killRestartRunnerIDEnv      = "YOLO_KILL_RESTART_RUNNER_ID"
	killRestartBlockEnv         = "YOLO_KILL_RESTART_BLOCK_AFTER_LAND"
	killRestartExitAfterDoneEnv = "YOLO_KILL_RESTART_EXIT_AFTER_DONE"
	killRestartLandedMarkerEnv  = "YOLO_KILL_RESTART_LANDED_MARKER"
)

func TestKillRestartRequeuesAfterLandedProcessDiesWithoutDuplicateOriginMerge(t *testing.T) {
	if os.Getenv(killRestartHelperEnv) == "1" {
		return
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is required for kill/restart integration test: %v", err)
	}

	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("create helper home: %v", err)
	}
	sourceRepo, originRepo := setupKillRestartGitOrigin(t, root)
	environmentsPath := writeKillRestartEnvironments(t, root, sourceRepo)
	queuePath := filepath.Join(root, "queue.db")

	store, err := workqueue.Open(queuePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	payload, err := json.Marshal(workitem.ImplementPayload{
		TaskID:      "QS-G8-KILL-RESTART",
		Title:       "Kill restart recovery",
		Description: "Exercise queue lease recovery after a landed subprocess dies.",
		PromptContext: workitem.ImplementPromptContext{
			Prompt:   "Write the deterministic kill/restart test fixture file.",
			ParentID: "QS-G8",
		},
		BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("marshal implement payload: %v", err)
	}
	item, err := store.Submit(workitem.Submission{
		Kind:           workitem.KindImplement,
		Source:         "kill-restart-source",
		SourceRef:      "QS-G8-KILL-RESTART",
		IdempotencyKey: "kill-restart-source/QS-G8-KILL-RESTART/implement",
		Preset:         "linux",
		Payload:        payload,
		MaxAttempts:    2,
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(initial store) error = %v", err)
	}

	landedMarker := filepath.Join(root, "first-run-landed")
	first := startKillRestartRunnerProcess(t, killRestartRunnerProcessConfig{
		queuePath:        queuePath,
		environmentsPath: environmentsPath,
		home:             home,
		runnerID:         "kill-restart-first",
		landedMarker:     landedMarker,
		blockAfterLand:   true,
	})
	waitForKillRestartLandedMarker(t, first, landedMarker, 15*time.Second)
	killKillRestartRunnerProcess(t, first)

	if got := countKillRestartOriginMerges(t, originRepo); got != 1 {
		t.Fatalf("origin merge commits after killed landed subprocess = %d, want 1", got)
	}
	if got := countKillRestartResultRows(t, queuePath, item.ID); got != 0 {
		t.Fatalf("work_results rows after killed landed subprocess = %d, want 0", got)
	}

	second := startKillRestartRunnerProcess(t, killRestartRunnerProcessConfig{
		queuePath:        queuePath,
		environmentsPath: environmentsPath,
		home:             home,
		runnerID:         "kill-restart-second",
		exitAfterDone:    true,
	})
	waitForKillRestartRequeue(t, queuePath, item.ID, 10*time.Second)
	// RequeueStale uses minute-scale production backoff. Once the test has
	// observed the lease requeue, clear it so the recovery subprocess can claim.
	clearKillRestartRequeueBackoff(t, queuePath, item.ID)
	waitForKillRestartRunnerProcess(t, second, 15*time.Second)
	waitForKillRestartCompletion(t, queuePath, item.ID, 2*time.Second)

	if got := countKillRestartOriginMerges(t, originRepo); got != 1 {
		t.Fatalf("origin merge commits after restart completion = %d, want exactly one landed merge", got)
	}
	if got := countKillRestartResultRows(t, queuePath, item.ID); got != 1 {
		state, attempt, claimedBy, notBefore := readKillRestartItemLease(t, queuePath, item.ID)
		t.Fatalf("work_results rows after restart completion = %d, want 1; item state=%q attempt=%d claimed_by=%q not_before=%q", got, state, attempt, claimedBy, notBefore)
	}
	if got := killRestartGit(t, root, "--git-dir", originRepo, "show", "main:implemented.txt"); got != "kill/restart landed exactly once" {
		t.Fatalf("origin implemented.txt = %q", got)
	}

	store, err = workqueue.Open(queuePath)
	if err != nil {
		t.Fatalf("Open(final store) error = %v", err)
	}
	defer store.Close()
	results, err := store.ListUnconsumedResults("kill-restart-source")
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
		t.Fatalf("item state = %q, want done; result status=%q payload=%s", got.Item.State, got.Result.Status, got.Result.Payload)
	}
	if got.Item.Attempt != 2 {
		t.Fatalf("item attempt = %d, want 2 after stale lease requeue and restart claim", got.Item.Attempt)
	}
	if got.Result.Status != workqueue.ResultStatusCompleted {
		t.Fatalf("result status = %q, want completed", got.Result.Status)
	}
	implementResult, err := workitem.DecodeImplementResult(got.Result.Payload)
	if err != nil {
		t.Fatalf("DecodeImplementResult(%s) error = %v", got.Result.Payload, err)
	}
	if implementResult.Status != string(contracts.RunnerResultCompleted) {
		t.Fatalf("implement result status = %q, want completed", implementResult.Status)
	}
	if implementResult.ReviewVerdict != "pass" {
		t.Fatalf("review verdict = %q, want pass", implementResult.ReviewVerdict)
	}
}

func TestKillRestartRunnerSubprocess(t *testing.T) {
	if os.Getenv(killRestartHelperEnv) != "1" {
		return
	}
	if err := runKillRestartRunnerSubprocess(); err != nil {
		t.Fatal(err)
	}
}

func runKillRestartRunnerSubprocess() error {
	cfg := runnerDaemonCommandConfig{
		queuePath:         os.Getenv(killRestartQueueEnv),
		environmentsPath:  os.Getenv(killRestartEnvironmentsEnv),
		presets:           []string{"linux"},
		runnerID:          os.Getenv(killRestartRunnerIDEnv),
		once:              os.Getenv(killRestartExitAfterDoneEnv) != "1",
		capacity:          1,
		pollInterval:      200 * time.Millisecond,
		heartbeatInterval: 250 * time.Millisecond,
		leaseTTL:          2 * time.Second,
	}
	normalized, err := normalizeRunnerDaemonConfig(cfg)
	if err != nil {
		return err
	}
	cfg = normalized

	lock, err := acquireRunnerDaemonLock(cfg.lockPath, cfg.runnerID)
	if err != nil {
		return err
	}
	defer func() {
		_ = lock.Release()
	}()

	store, err := workqueue.Open(cfg.queuePath)
	if err != nil {
		return err
	}
	defer store.Close()

	runners, err := openRunnerRegistry(cfg.queuePath)
	if err != nil {
		return err
	}
	defer runners.Close()
	if err := runners.Register(cfg.runnerID, cfg.presets, cfg.capacity); err != nil {
		return err
	}

	presets, err := loadRunnerEnvironmentPresets(cfg.environmentsPath, cfg.presets)
	if err != nil {
		return err
	}
	events := &killRestartBlockingEventSink{
		inner:          defaultRunnerDaemonEventSink(cfg.runnerID),
		blockAfterLand: os.Getenv(killRestartBlockEnv) == "1",
		landedMarker:   os.Getenv(killRestartLandedMarkerEnv),
	}
	runCtx := context.Background()
	if os.Getenv(killRestartExitAfterDoneEnv) == "1" {
		ctx, cancel := context.WithCancel(runCtx)
		defer cancel()
		runCtx = ctx
		events.cancelAfterItemDone = cancel
	}
	daemon := runnerDaemon{
		store:   store,
		runners: runners,
		handlers: runnerKindRegistry{
			workitem.KindImplement: newRunnerImplementKindHandler(func(_ context.Context, item workitem.Item, _ envpreset.Workspace) (runnerImplementExecutor, error) {
				preset, ok := presets[item.Preset]
				if !ok {
					return runnerImplementExecutor{}, fmt.Errorf("preset %q not loaded", item.Preset)
				}
				landing, err := envpreset.ResolveLanding(preset)
				if err != nil {
					return runnerImplementExecutor{}, err
				}
				return runnerImplementExecutor{
					Runner: killRestartFakeAgent{},
					Agent: envpreset.ResolvedAgent{
						Backend:          "kill-restart-fake",
						Model:            "kill-restart-fake",
						RunnerTimeout:    5 * time.Second,
						WatchdogTimeout:  time.Second,
						WatchdogInterval: 250 * time.Millisecond,
					},
					Landing: landing,
					Events:  events,
				}, nil
			}),
		},
		events:             events,
		environmentPresets: presets,
		materialize:        envpreset.Materialize,
		cfg:                cfg,
	}
	err = daemon.Run(runCtx)
	if os.Getenv(killRestartExitAfterDoneEnv) == "1" && errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

type killRestartFakeAgent struct{}

func (killRestartFakeAgent) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	switch request.Mode {
	case contracts.RunnerModeImplement:
		if err := os.WriteFile(filepath.Join(request.RepoRoot, "implemented.txt"), []byte("kill/restart landed exactly once\n"), 0o644); err != nil {
			return contracts.RunnerResult{}, err
		}
		return contracts.RunnerResult{
			Status:    contracts.RunnerResultCompleted,
			Artifacts: map[string]string{"backend": "kill-restart-fake"},
		}, nil
	case contracts.RunnerModeReview:
		return contracts.RunnerResult{
			Status:      contracts.RunnerResultCompleted,
			ReviewReady: true,
			Artifacts:   map[string]string{"backend": "kill-restart-fake", "review_verdict": "pass"},
		}, nil
	default:
		return contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: "unexpected runner mode " + string(request.Mode)}, nil
	}
}

type killRestartBlockingEventSink struct {
	inner               contracts.EventSink
	blockAfterLand      bool
	landedMarker        string
	cancelAfterItemDone context.CancelFunc
}

func (s *killRestartBlockingEventSink) Emit(ctx context.Context, event contracts.Event) error {
	if s.inner != nil {
		if err := s.inner.Emit(ctx, event); err != nil {
			return err
		}
	}
	if !s.blockAfterLand || event.Type != contracts.EventTypeMergeLanded {
		if s.cancelAfterItemDone != nil && event.Type == contracts.EventTypeRunnerFinished && strings.TrimSpace(event.ItemID) != "" {
			s.cancelAfterItemDone()
		}
		return nil
	}
	if strings.TrimSpace(s.landedMarker) != "" {
		if err := os.WriteFile(s.landedMarker, []byte("landed\n"), 0o644); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

type killRestartRunnerProcessConfig struct {
	queuePath        string
	environmentsPath string
	home             string
	runnerID         string
	landedMarker     string
	blockAfterLand   bool
	exitAfterDone    bool
}

type killRestartRunnerProcess struct {
	cmd    *exec.Cmd
	stdout bytes.Buffer
	stderr bytes.Buffer
	done   chan error
}

func startKillRestartRunnerProcess(t *testing.T, cfg killRestartRunnerProcessConfig) *killRestartRunnerProcess {
	t.Helper()

	cmd := exec.Command(os.Args[0], "-test.run=^TestKillRestartRunnerSubprocess$", "-test.v")
	cmd.Env = append(os.Environ(),
		killRestartHelperEnv+"=1",
		killRestartQueueEnv+"="+cfg.queuePath,
		killRestartEnvironmentsEnv+"="+cfg.environmentsPath,
		killRestartHomeEnv+"="+cfg.home,
		killRestartRunnerIDEnv+"="+cfg.runnerID,
		killRestartLandedMarkerEnv+"="+cfg.landedMarker,
		"HOME="+cfg.home,
		"GIT_AUTHOR_NAME=Kill Restart Test",
		"GIT_AUTHOR_EMAIL=kill-restart@example.test",
		"GIT_COMMITTER_NAME=Kill Restart Test",
		"GIT_COMMITTER_EMAIL=kill-restart@example.test",
		"GIT_TERMINAL_PROMPT=0",
	)
	if cfg.blockAfterLand {
		cmd.Env = append(cmd.Env, killRestartBlockEnv+"=1")
	}
	if cfg.exitAfterDone {
		cmd.Env = append(cmd.Env, killRestartExitAfterDoneEnv+"=1")
	}

	proc := &killRestartRunnerProcess{cmd: cmd, done: make(chan error, 1)}
	cmd.Stdout = &proc.stdout
	cmd.Stderr = &proc.stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start runner subprocess %q: %v", cfg.runnerID, err)
	}
	go func() {
		proc.done <- cmd.Wait()
		close(proc.done)
	}()
	t.Cleanup(func() {
		if cmd.ProcessState != nil {
			return
		}
		_ = cmd.Process.Kill()
		select {
		case <-proc.done:
		case <-time.After(2 * time.Second):
		}
	})
	return proc
}

func waitForKillRestartLandedMarker(t *testing.T, proc *killRestartRunnerProcess, marker string, timeout time.Duration) {
	t.Helper()

	deadline := time.After(timeout)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		select {
		case err := <-proc.done:
			t.Fatalf("runner subprocess exited before merge_landed marker: %v\nstdout:\n%s\nstderr:\n%s", err, proc.stdout.String(), proc.stderr.String())
		case <-deadline:
			t.Fatalf("timed out waiting for merge_landed marker %s\nstdout:\n%s\nstderr:\n%s", marker, proc.stdout.String(), proc.stderr.String())
		case <-ticker.C:
		}
	}
}

func killKillRestartRunnerProcess(t *testing.T, proc *killRestartRunnerProcess) {
	t.Helper()

	if proc.cmd.Process == nil {
		t.Fatal("runner subprocess has no process")
	}
	if err := proc.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill runner subprocess: %v\nstdout:\n%s\nstderr:\n%s", err, proc.stdout.String(), proc.stderr.String())
	}
	select {
	case err := <-proc.done:
		if err == nil {
			t.Fatalf("killed runner subprocess exited successfully; expected kill\nstdout:\n%s\nstderr:\n%s", proc.stdout.String(), proc.stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for killed runner subprocess\nstdout:\n%s\nstderr:\n%s", proc.stdout.String(), proc.stderr.String())
	}
}

func waitForKillRestartRunnerProcess(t *testing.T, proc *killRestartRunnerProcess, timeout time.Duration) {
	t.Helper()

	select {
	case err := <-proc.done:
		if err != nil {
			t.Fatalf("runner subprocess error: %v\nstdout:\n%s\nstderr:\n%s", err, proc.stdout.String(), proc.stderr.String())
		}
	case <-time.After(timeout):
		_ = proc.cmd.Process.Kill()
		t.Fatalf("timed out waiting for runner subprocess\nstdout:\n%s\nstderr:\n%s", proc.stdout.String(), proc.stderr.String())
	}
}

func setupKillRestartGitOrigin(t *testing.T, root string) (string, string) {
	t.Helper()

	originRepo := filepath.Join(root, "origin.git")
	sourceRepo := filepath.Join(root, "source")
	killRestartGit(t, root, "init", "--bare", originRepo)
	killRestartGit(t, root, "init", sourceRepo)
	killRestartGit(t, sourceRepo, "config", "user.name", "Kill Restart Test")
	killRestartGit(t, sourceRepo, "config", "user.email", "kill-restart@example.test")
	if err := os.WriteFile(filepath.Join(sourceRepo, "README.md"), []byte("initial\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	killRestartGit(t, sourceRepo, "add", "README.md")
	killRestartGit(t, sourceRepo, "commit", "-m", "initial")
	killRestartGit(t, sourceRepo, "branch", "-M", "main")
	killRestartGit(t, sourceRepo, "remote", "add", "origin", originRepo)
	killRestartGit(t, sourceRepo, "push", "-u", "origin", "main")
	return sourceRepo, originRepo
}

func writeKillRestartEnvironments(t *testing.T, root string, sourceRepo string) string {
	t.Helper()

	path := filepath.Join(root, "environments.yaml")
	content := "presets:\n" +
		"  linux:\n" +
		"    workspace:\n" +
		"      strategy: git-clone\n" +
		"      origin: " + strconv.Quote(sourceRepo) + "\n" +
		"      base_branch: main\n" +
		"    landing:\n" +
		"      type: git-merge\n" +
		"    agent:\n" +
		"      backend: codex-cli\n" +
		"      model: kill-restart-fake\n" +
		"      runner_timeout: 5s\n" +
		"      watchdog_timeout: 1s\n" +
		"      watchdog_interval: 20ms\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write environments file: %v", err)
	}
	return path
}

func waitForKillRestartRequeue(t *testing.T, queuePath string, itemID string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		state, attempt, claimedBy, notBefore := readKillRestartItemLease(t, queuePath, itemID)
		if state == "pending" && attempt == 1 && claimedBy == "" && strings.TrimSpace(notBefore) != "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for stale lease requeue; state=%q attempt=%d claimed_by=%q not_before=%q", state, attempt, claimedBy, notBefore)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func clearKillRestartRequeueBackoff(t *testing.T, queuePath string, itemID string) {
	t.Helper()

	db, err := sql.Open("sqlite", queuePath)
	if err != nil {
		t.Fatalf("open queue db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		t.Fatalf("configure queue db busy_timeout: %v", err)
	}
	result, err := db.Exec("UPDATE work_items SET not_before = '', updated_at = ? WHERE id = ? AND state = 'pending'", time.Now().UTC().Format(time.RFC3339Nano), itemID)
	if err != nil {
		t.Fatalf("clear requeue backoff: %v", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("clear requeue backoff rows affected: %v", err)
	}
	if affected != 1 {
		t.Fatalf("clear requeue backoff affected %d rows, want 1", affected)
	}
}

func waitForKillRestartCompletion(t *testing.T, queuePath string, itemID string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		state, attempt, claimedBy, notBefore := readKillRestartItemLease(t, queuePath, itemID)
		if state == "done" && countKillRestartResultRows(t, queuePath, itemID) == 1 {
			return
		}
		if state == "failed" {
			t.Fatalf("item failed during restart completion; attempt=%d claimed_by=%q not_before=%q payload=%s", attempt, claimedBy, notBefore, readKillRestartResultPayload(t, queuePath, itemID))
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for restart completion; state=%q attempt=%d claimed_by=%q not_before=%q", state, attempt, claimedBy, notBefore)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func readKillRestartItemLease(t *testing.T, queuePath string, itemID string) (string, int, string, string) {
	t.Helper()

	db, err := sql.Open("sqlite", queuePath)
	if err != nil {
		t.Fatalf("open queue db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		t.Fatalf("configure queue db busy_timeout: %v", err)
	}
	var state string
	var attempt int
	var claimedBy string
	var notBefore string
	if err := db.QueryRow("SELECT state, attempt, claimed_by, not_before FROM work_items WHERE id = ?", itemID).Scan(&state, &attempt, &claimedBy, &notBefore); err != nil {
		t.Fatalf("read item lease state: %v", err)
	}
	return state, attempt, claimedBy, notBefore
}

func readKillRestartResultPayload(t *testing.T, queuePath string, itemID string) string {
	t.Helper()

	db, err := sql.Open("sqlite", queuePath)
	if err != nil {
		t.Fatalf("open queue db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		t.Fatalf("configure queue db busy_timeout: %v", err)
	}
	var payload string
	if err := db.QueryRow("SELECT payload FROM work_results WHERE item_id = ?", itemID).Scan(&payload); err != nil {
		t.Fatalf("read result payload: %v", err)
	}
	return payload
}

func countKillRestartResultRows(t *testing.T, queuePath string, itemID string) int {
	t.Helper()

	db, err := sql.Open("sqlite", queuePath)
	if err != nil {
		t.Fatalf("open queue db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		t.Fatalf("configure queue db busy_timeout: %v", err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM work_results WHERE item_id = ?", itemID).Scan(&count); err != nil {
		t.Fatalf("count work_results: %v", err)
	}
	return count
}

func countKillRestartOriginMerges(t *testing.T, originRepo string) int {
	t.Helper()

	raw := killRestartGit(t, "", "--git-dir", originRepo, "rev-list", "--merges", "--count", "main")
	count, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		t.Fatalf("parse origin merge count %q: %v", raw, err)
	}
	return count
}

func killRestartGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	if strings.TrimSpace(dir) != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Kill Restart Test",
		"GIT_AUTHOR_EMAIL=kill-restart@example.test",
		"GIT_COMMITTER_NAME=Kill Restart Test",
		"GIT_COMMITTER_EMAIL=kill-restart@example.test",
		"GIT_TERMINAL_PROMPT=0",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed in %q: %s: %v", strings.Join(args, " "), dir, strings.TrimSpace(string(output)), err)
	}
	return strings.TrimSpace(string(output))
}
