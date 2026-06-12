package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	stdexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/envpreset"
	yexec "github.com/egv/yolo-runner/v2/internal/exec"
	gitvcs "github.com/egv/yolo-runner/v2/internal/vcs/git"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestKillRestartRequeuesAndCompletesWithoutDuplicateLanding(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGKILL restart integration test uses Unix process-kill semantics")
	}
	if _, err := stdexec.LookPath("git"); err != nil {
		t.Skip("git CLI is required for kill/restart integration test")
	}

	root, repo := initKillRestartGitRepo(t)
	dbPath := filepath.Join(root, "queue.db")
	markerPath := filepath.Join(root, "first-runner-blocked-at-push")

	store, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	payload := marshalRunnerImplementPayload(t, workitem.ImplementPayload{
		TaskID:      "KR-1",
		Title:       "Kill/restart recovery",
		Description: "Prove a killed runner lease is requeued and the next process finishes once.",
		PromptContext: workitem.ImplementPromptContext{
			Prompt:   "Write the kill-restart marker file.",
			ParentID: "ROOT-KR",
		},
	})
	item, err := store.Submit(workitem.Submission{
		Kind:           workitem.KindImplement,
		Source:         "kill-restart-source",
		SourceRef:      "KR-1",
		IdempotencyKey: "kill-restart-source/KR-1/implement",
		Preset:         "linux",
		Payload:        payload,
		MaxAttempts:    3,
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(seed store) error = %v", err)
	}

	first := startKillRestartRunnerSubprocess(t, killRestartRunnerSubprocessConfig{
		dbPath:     dbPath,
		repoPath:   repo,
		mode:       "crash",
		runnerID:   "kill-restart-first",
		markerPath: markerPath,
	})
	waitForKillRestartFile(t, markerPath, 10*time.Second)
	if err := first.cmd.Process.Kill(); err != nil {
		t.Fatalf("SIGKILL first runner: %v\n%s", err, first.output.String())
	}
	if err := waitKillRestartSubprocess(t, first, 5*time.Second); err == nil {
		t.Fatalf("first runner exited cleanly, want killed\n%s", first.output.String())
	}

	waitForKillRestartRequeue(t, dbPath, item.ID, 5*time.Second)
	assertKillRestartItemState(t, dbPath, item.ID, "pending", 1)
	assertKillRestartResultCount(t, dbPath, item.ID, 0)
	clearKillRestartNotBefore(t, dbPath, item.ID)

	second := startKillRestartRunnerSubprocess(t, killRestartRunnerSubprocessConfig{
		dbPath:   dbPath,
		repoPath: repo,
		mode:     "complete",
		runnerID: "kill-restart-second",
	})
	if err := waitKillRestartSubprocess(t, second, 15*time.Second); err != nil {
		t.Fatalf("second runner failed: %v\n%s", err, second.output.String())
	}

	assertKillRestartItemState(t, dbPath, item.ID, "done", 2)
	assertKillRestartResultCount(t, dbPath, item.ID, 1)
	assertKillRestartCompletedResult(t, dbPath, item.ID)
	assertKillRestartGitLandingIsIdempotent(t, repo)
}

func TestKillRestartRunnerSubprocess(t *testing.T) {
	if os.Getenv("YOLO_KILL_RESTART_HELPER") != "1" {
		return
	}
	if err := runKillRestartRunnerSubprocess(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type killRestartRunnerSubprocessConfig struct {
	dbPath     string
	repoPath   string
	mode       string
	runnerID   string
	markerPath string
}

type killRestartSubprocess struct {
	cmd    *stdexec.Cmd
	output *bytes.Buffer
}

func startKillRestartRunnerSubprocess(t *testing.T, cfg killRestartRunnerSubprocessConfig) killRestartSubprocess {
	t.Helper()

	cmd := stdexec.Command(os.Args[0], "-test.run=TestKillRestartRunnerSubprocess", "-test.v")
	cmd.Env = append(os.Environ(),
		"YOLO_KILL_RESTART_HELPER=1",
		"YOLO_KILL_RESTART_DB="+cfg.dbPath,
		"YOLO_KILL_RESTART_REPO="+cfg.repoPath,
		"YOLO_KILL_RESTART_MODE="+cfg.mode,
		"YOLO_KILL_RESTART_RUNNER_ID="+cfg.runnerID,
		"YOLO_KILL_RESTART_MARKER="+cfg.markerPath,
		"GIT_MERGE_AUTOEDIT=no",
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper runner %q: %v", cfg.mode, err)
	}
	return killRestartSubprocess{cmd: cmd, output: &output}
}

func waitKillRestartSubprocess(t *testing.T, proc killRestartSubprocess, timeout time.Duration) error {
	t.Helper()

	done := make(chan error, 1)
	go func() {
		done <- proc.cmd.Wait()
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		_ = proc.cmd.Process.Kill()
		t.Fatalf("helper runner timed out after %s\n%s", timeout, proc.output.String())
		return nil
	}
}

func runKillRestartRunnerSubprocess(ctx context.Context) error {
	dbPath := strings.TrimSpace(os.Getenv("YOLO_KILL_RESTART_DB"))
	repoPath := strings.TrimSpace(os.Getenv("YOLO_KILL_RESTART_REPO"))
	mode := strings.TrimSpace(os.Getenv("YOLO_KILL_RESTART_MODE"))
	runnerID := strings.TrimSpace(os.Getenv("YOLO_KILL_RESTART_RUNNER_ID"))
	if dbPath == "" || repoPath == "" || mode == "" || runnerID == "" {
		return fmt.Errorf("missing kill/restart helper environment")
	}

	store, err := workqueue.Open(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	runners, err := openRunnerRegistry(dbPath)
	if err != nil {
		return err
	}
	defer runners.Close()
	if err := runners.Register(runnerID, []string{"linux"}, 1); err != nil {
		return err
	}

	daemon := runnerDaemon{
		store:   store,
		runners: runners,
		events:  runnerDaemonNoopEventSink{},
		handlers: runnerKindRegistry{
			workitem.KindImplement: newRunnerImplementKindHandler(func(context.Context, workitem.Item, envpreset.Workspace) (runnerImplementExecutor, error) {
				return runnerImplementExecutor{
					Runner: &killRestartFileAgent{},
					Agent: envpreset.ResolvedAgent{
						Backend:          "fake",
						Model:            "kill-restart",
						RunnerTimeout:    5 * time.Second,
						WatchdogTimeout:  time.Second,
						WatchdogInterval: 100 * time.Millisecond,
					},
					Landing: envpreset.LandingTypeGitMerge,
				}, nil
			}),
		},
		environmentPresets: map[string]envpreset.Preset{
			"linux": {
				Workspace: envpreset.Workspace{Strategy: envpreset.WorkspaceStrategyPath, Path: repoPath},
				Landing:   envpreset.Landing{Type: envpreset.LandingTypeGitMerge},
			},
		},
		materialize: func(context.Context, envpreset.Preset, string) (envpreset.Workspace, error) {
			return envpreset.Workspace{
				Path: repoPath,
				VCS:  newKillRestartVCS(repoPath, mode, os.Getenv("YOLO_KILL_RESTART_MARKER")),
			}, nil
		},
		cfg: runnerDaemonCommandConfig{
			queuePath:         dbPath,
			presets:           []string{"linux"},
			runnerID:          runnerID,
			once:              true,
			pollInterval:      10 * time.Millisecond,
			heartbeatInterval: 50 * time.Millisecond,
			leaseTTL:          200 * time.Millisecond,
		},
	}
	return daemon.Run(ctx)
}

type killRestartFileAgent struct{}

func (a *killRestartFileAgent) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	switch request.Mode {
	case contracts.RunnerModeImplement:
		path := filepath.Join(request.RepoRoot, "kill-restart.txt")
		if err := os.WriteFile(path, []byte("landed once\n"), 0o644); err != nil {
			return contracts.RunnerResult{}, err
		}
		return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
	case contracts.RunnerModeReview:
		return contracts.RunnerResult{
			Status:      contracts.RunnerResultCompleted,
			ReviewReady: true,
			Artifacts:   map[string]string{"review_verdict": "pass"},
		}, nil
	default:
		return contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: "unexpected mode " + string(request.Mode)}, nil
	}
}

type killRestartVCS struct {
	inner      contracts.VCS
	mode       string
	markerPath string
}

func newKillRestartVCS(repoPath string, mode string, markerPath string) *killRestartVCS {
	runner := yexec.NewCommandRunner(filepath.Join(repoPath, ".yolo-test-logs"), io.Discard)
	return &killRestartVCS{
		inner:      gitvcs.NewVCSAdapter(gitvcs.NewGitCommandAdapter(runner)),
		mode:       mode,
		markerPath: markerPath,
	}
}

func (v *killRestartVCS) EnsureMain(ctx context.Context) error {
	return v.inner.EnsureMain(ctx)
}

func (v *killRestartVCS) CreateTaskBranch(ctx context.Context, taskID string) (string, error) {
	return v.inner.CreateTaskBranch(ctx, taskID)
}

func (v *killRestartVCS) Checkout(ctx context.Context, ref string) error {
	return v.inner.Checkout(ctx, ref)
}

func (v *killRestartVCS) CommitAll(ctx context.Context, message string) (string, error) {
	return v.inner.CommitAll(ctx, message)
}

func (v *killRestartVCS) MergeToMain(ctx context.Context, sourceBranch string) error {
	return v.inner.MergeToMain(ctx, sourceBranch)
}

func (v *killRestartVCS) PushBranch(ctx context.Context, branch string) error {
	return v.inner.PushBranch(ctx, branch)
}

func (v *killRestartVCS) PushMain(ctx context.Context) error {
	if v.mode == "crash" {
		if strings.TrimSpace(v.markerPath) == "" {
			return fmt.Errorf("crash mode marker path is required")
		}
		if err := os.WriteFile(v.markerPath, []byte("blocked at push\n"), 0o644); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Second):
			return fmt.Errorf("crash-mode runner was not killed")
		}
	}
	return v.inner.PushMain(ctx)
}

func initKillRestartGitRepo(t *testing.T) (string, string) {
	t.Helper()

	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	repo := filepath.Join(root, "workspace")
	runCommand(t, root, "git", "init", "--bare", origin)
	runCommand(t, root, "git", "clone", origin, repo)
	runCommand(t, repo, "git", "checkout", "-b", "main")
	runCommand(t, repo, "git", "config", "user.name", "Kill Restart Test")
	runCommand(t, repo, "git", "config", "user.email", "kill-restart@example.com")
	runCommand(t, repo, "git", "config", "core.editor", "true")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runCommand(t, repo, "git", "add", "README.md")
	runCommand(t, repo, "git", "commit", "-m", "init")
	runCommand(t, repo, "git", "push", "-u", "origin", "main")
	return root, repo
}

func waitForKillRestartFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", path, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForKillRestartRequeue(t *testing.T, dbPath string, itemID string, timeout time.Duration) {
	t.Helper()

	store, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open(reaper) error = %v", err)
	}
	defer store.Close()

	deadline := time.Now().Add(timeout)
	for {
		requeued, err := store.RequeueStale(time.Now().UTC())
		if err != nil {
			t.Fatalf("RequeueStale() error = %v", err)
		}
		state, attempt := readKillRestartItemState(t, dbPath, itemID)
		if requeued > 0 || state == "pending" && attempt == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for stale lease requeue; state=%q attempt=%d", state, attempt)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func assertKillRestartItemState(t *testing.T, dbPath string, itemID string, wantState string, wantAttempt int) {
	t.Helper()

	gotState, gotAttempt := readKillRestartItemState(t, dbPath, itemID)
	if gotState != wantState || gotAttempt != wantAttempt {
		t.Fatalf("item state/attempt = %q/%d, want %q/%d", gotState, gotAttempt, wantState, wantAttempt)
	}
}

func readKillRestartItemState(t *testing.T, dbPath string, itemID string) (string, int) {
	t.Helper()

	db := openKillRestartSQL(t, dbPath)
	defer db.Close()

	var state string
	var attempt int
	if err := db.QueryRow("SELECT state, attempt FROM work_items WHERE id = ?", itemID).Scan(&state, &attempt); err != nil {
		t.Fatalf("read item %s state: %v", itemID, err)
	}
	return state, attempt
}

func assertKillRestartResultCount(t *testing.T, dbPath string, itemID string, want int) {
	t.Helper()

	db := openKillRestartSQL(t, dbPath)
	defer db.Close()

	var got int
	if err := db.QueryRow("SELECT COUNT(*) FROM work_results WHERE item_id = ?", itemID).Scan(&got); err != nil {
		t.Fatalf("count work results for %s: %v", itemID, err)
	}
	if got != want {
		t.Fatalf("work result count for %s = %d, want %d", itemID, got, want)
	}
}

func clearKillRestartNotBefore(t *testing.T, dbPath string, itemID string) {
	t.Helper()

	db := openKillRestartSQL(t, dbPath)
	defer db.Close()

	result, err := db.Exec("UPDATE work_items SET not_before = '', updated_at = ? WHERE id = ?", time.Now().UTC().Format(time.RFC3339Nano), itemID)
	if err != nil {
		t.Fatalf("clear not_before for %s: %v", itemID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("clear not_before rows affected for %s: %v", itemID, err)
	}
	if affected != 1 {
		t.Fatalf("clear not_before affected %d rows, want 1", affected)
	}
}

func assertKillRestartCompletedResult(t *testing.T, dbPath string, itemID string) {
	t.Helper()

	store, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open(result store) error = %v", err)
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
	if got.Item.ID != itemID {
		t.Fatalf("result item ID = %q, want %q", got.Item.ID, itemID)
	}
	if got.Result.Status != workqueue.ResultStatusCompleted {
		t.Fatalf("result status = %q, want completed", got.Result.Status)
	}
	var payload workitem.ImplementResult
	if err := json.Unmarshal(got.Result.Payload, &payload); err != nil {
		t.Fatalf("unmarshal implement result %s: %v", got.Result.Payload, err)
	}
	if payload.Status != string(contracts.RunnerResultCompleted) {
		t.Fatalf("implement result status = %q, want completed", payload.Status)
	}
	if payload.Branch != "task/KR-1" {
		t.Fatalf("implement result branch = %q, want task/KR-1", payload.Branch)
	}
	if payload.ReviewVerdict != "pass" {
		t.Fatalf("implement review verdict = %q, want pass", payload.ReviewVerdict)
	}
	if strings.TrimSpace(payload.CommitSHA) == "" {
		t.Fatalf("implement commit SHA is empty")
	}
}

func assertKillRestartGitLandingIsIdempotent(t *testing.T, repo string) {
	t.Helper()

	mergeCount := strings.TrimSpace(gitOutput(t, repo, "rev-list", "--count", "--merges", "main"))
	if mergeCount != "1" {
		t.Fatalf("merge commit count on main = %s, want 1", mergeCount)
	}

	subjects := strings.Split(strings.TrimSpace(gitOutput(t, repo, "log", "--all", "--format=%s")), "\n")
	autoCommits := 0
	for _, subject := range subjects {
		if subject == "chore(task): auto-commit before landing KR-1" {
			autoCommits++
		}
	}
	if autoCommits != 1 {
		t.Fatalf("auto-commit count for KR-1 = %d, want 1; subjects=%v", autoCommits, subjects)
	}

	fileCommits := strings.TrimSpace(gitOutput(t, repo, "log", "--format=%H", "--", "kill-restart.txt"))
	if got := len(nonEmptyKillRestartLines(fileCommits)); got != 1 {
		t.Fatalf("kill-restart.txt commit count = %d, want 1; commits=%q", got, fileCommits)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := stdexec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v (%s)", args, err, string(output))
	}
	return string(output)
}

func nonEmptyKillRestartLines(raw string) []string {
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func openKillRestartSQL(t *testing.T, dbPath string) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite %s: %v", dbPath, err)
	}
	db.SetMaxOpenConns(1)
	return db
}
