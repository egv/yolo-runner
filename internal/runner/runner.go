package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/anomalyco/yolo-runner/internal/opencode"
	"github.com/anomalyco/yolo-runner/internal/ui"
)

type Bead struct {
	ID                 string
	Title              string
	Description        string
	AcceptanceCriteria string
	Status             string
}

type BeadsClient interface {
	Ready(rootID string) (Issue, error)
	Tree(rootID string) (Issue, error)
	Show(id string) (Bead, error)
	UpdateStatus(id string, status string) error
	UpdateStatusWithReason(id string, status string, reason string) error
	Close(id string) error
	CloseEligible() error
	Sync() error
}

type PromptBuilder interface {
	Build(issueID string, title string, description string, acceptance string) string
}

type OpenCodeRunner interface {
	Run(issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string) error
}

type OpenCodeContextRunner interface {
	RunWithContext(ctx context.Context, issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string) error
}

type GitClient interface {
	AddAll() error
	IsDirty() (bool, error)
	Commit(message string) error
	RevParseHead() (string, error)
}

type Logger interface {
	AppendRunnerSummary(repoRoot string, issueID string, title string, status string, commitSHA string) error
}

type RunOnceDeps struct {
	// Legacy interfaces for backward compatibility
	Beads    BeadsClient
	OpenCode OpenCodeRunner

	// New interface-driven dependencies
	TaskTracker TaskTracker
	CodingAgent CodingAgent

	// Other dependencies
	Prompt PromptBuilder
	Git    GitClient
	Logger Logger
	Events EventEmitter
}

type RunOnceOptions struct {
	RepoRoot        string
	RootID          string
	Model           string
	ConfigRoot      string
	ConfigDir       string
	LogPath         string
	DryRun          bool
	Out             io.Writer
	ProgressNow     func() time.Time
	ProgressTicker  func() (<-chan time.Time, func())
	Progress        ProgressState
	Stop            <-chan struct{}
	CleanupConfirm  func(summary string) (bool, error)
	StatusPorcelain func(context.Context) (string, error)
	GitRestoreAll   func(context.Context) error
	GitCleanAll     func(context.Context) error
}

type ProgressState struct {
	Completed int
	Total     int
}

var now = time.Now

const maxStallReasonLength = 512

func sanitizeStallReason(reason string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '|', '\t', '\n', '\r':
			return ' '
		}
		if r < 32 {
			return ' '
		}
		return r
	}, reason)
}

type cleanupGit struct {
	ctx       context.Context
	statusFn  func(context.Context) (string, error)
	restoreFn func(context.Context) error
	cleanFn   func(context.Context) error
}

func (c cleanupGit) StatusPorcelain() (string, error) {
	if c.statusFn == nil {
		return "", nil
	}
	if c.ctx == nil {
		return c.statusFn(context.Background())
	}
	return c.statusFn(c.ctx)
}

func (c cleanupGit) RestoreAll() error {
	if c.restoreFn == nil {
		return nil
	}
	if c.ctx == nil {
		return c.restoreFn(context.Background())
	}
	return c.restoreFn(c.ctx)
}

func (c cleanupGit) CleanAll() error {
	if c.cleanFn == nil {
		return nil
	}
	if c.ctx == nil {
		return c.cleanFn(context.Background())
	}
	return c.cleanFn(c.ctx)
}

type progressTickerAdapter struct {
	ch   <-chan time.Time
	stop func()
}

func (p progressTickerAdapter) C() <-chan time.Time {
	return p.ch
}

func (p progressTickerAdapter) Stop() {
	if p.stop != nil {
		p.stop()
	}
}

func progressTickerFrom(fn func() (<-chan time.Time, func())) ui.ProgressTicker {
	if fn == nil {
		return nil
	}
	ch, stop := fn()
	return progressTickerAdapter{ch: ch, stop: stop}
}

// Helper functions to use new interfaces when available, falling back to legacy interfaces
func getTaskTracker(deps RunOnceDeps) TaskTracker {
	if deps.TaskTracker != nil {
		return deps.TaskTracker
	}
	return nil
}

func getLegacyBeadsClient(deps RunOnceDeps) BeadsClient {
	if deps.Beads != nil {
		return deps.Beads
	}
	if deps.TaskTracker != nil {
		return NewTaskTrackerAdapter(deps.TaskTracker)
	}
	return nil
}

func getCodingAgent(deps RunOnceDeps) CodingAgent {
	if deps.CodingAgent != nil {
		return deps.CodingAgent
	}
	return nil
}

func getLegacyOpenCodeRunner(deps RunOnceDeps) OpenCodeRunner {
	if deps.OpenCode != nil {
		return deps.OpenCode
	}
	if deps.CodingAgent != nil {
		return NewCodingAgentAdapter(deps.CodingAgent)
	}
	return nil
}

func RunOnce(opts RunOnceOptions, deps RunOnceDeps) (string, error) {
	out := opts.Out
	if out == nil {
		out = io.Discard
	}

	// Use legacy interface for backward compatibility
	beadsClient := getLegacyBeadsClient(deps)
	if beadsClient == nil {
		return "", fmt.Errorf("no task tracker available")
	}

	root, err := beadsClient.Ready(opts.RootID)
	if err != nil {
		return "", err
	}

	progressState := opts.Progress
	var tree Issue
	treeLoaded := false
	if progressState.Total == 0 && beadsClient != nil {
		tree, err = beadsClient.Tree(opts.RootID)
		if err != nil {
			return "", err
		}
		treeLoaded = true
		progressState.Total = CountRunnableLeaves(tree)
	}

	leafID := SelectFirstOpenLeafTaskID(root)
	if leafID == "" {
		if !treeLoaded && beadsClient != nil {
			tree, err = beadsClient.Tree(opts.RootID)
			if err != nil {
				return "", err
			}
			treeLoaded = true
		}
		if treeLoaded && isRunnableLeaf(tree) && (tree.Status == "closed" || tree.Status == "blocked") {
			fmt.Fprintf(out, "Root issue %s is %s; no work to run\n", tree.ID, tree.Status)
			return "no_tasks", nil
		}
		fmt.Fprintln(out, "No tasks available")
		return "no_tasks", nil
	}

	startTime := now()

	bead, err := beadsClient.Show(leafID)
	if err != nil {
		return "", err
	}

	stopState := NewStopState()
	if opts.Stop != nil {
		stopState.MarkInProgress(leafID)
		stopState.Watch(opts.Stop)
	}
	stopCtx := stopState.Context()

	progressLabel := ""
	if progressState.Total > 0 {
		progressLabel = fmt.Sprintf("[%d/%d] ", progressState.Completed, progressState.Total)
	}
	fmt.Fprintf(out, "Starting %s%s: %s\n", progressLabel, leafID, bead.Title)

	emitPhase(deps.Events, EventSelectTask, leafID, bead.Title, progressState, "")

	prompt := deps.Prompt.Build(leafID, bead.Title, bead.Description, bead.AcceptanceCriteria)
	command := opencode.BuildArgs(opts.RepoRoot, prompt, opts.Model)

	if opts.DryRun {
		fmt.Fprintf(out, "Task: %s - %s\n", leafID, bead.Title)
		fmt.Fprintf(out, "Command: %s\n", strings.Join(opencode.RedactArgs(command), " "))
		return "dry_run", nil
	}

	currentProgress := progressState
	setState := func(state string) {
		fmt.Fprintf(out, "State: %s\n", state)
	}
	setState("selecting task")
	emitPhase(deps.Events, EventBeadsUpdate, leafID, bead.Title, currentProgress, "")
	if err := beadsClient.UpdateStatus(leafID, "in_progress"); err != nil {
		return "", err
	}

	state := "opencode running"
	setState(state)
	// Only run progress heartbeat in non-TUI mode (TUI has its own spinner in statusbar)
	var progress *ui.Progress
	var progressCtx context.Context
	var cancelProgress context.CancelFunc
	if deps.Events == nil {
		progress = ui.NewProgress(ui.ProgressConfig{
			Writer:  out,
			State:   state,
			LogPath: opts.LogPath,
			Ticker:  progressTickerFrom(opts.ProgressTicker),
			Now:     opts.ProgressNow,
		})
		progressCtx, cancelProgress = context.WithCancel(stopCtx)
		defer cancelProgress()
		go progress.Run(progressCtx)
	}

	emitPhase(deps.Events, EventOpenCodeStart, leafID, bead.Title, currentProgress, opts.Model)
	var openCodeErr error
	// Use legacy interface for backward compatibility
	openCodeRunner := getLegacyOpenCodeRunner(deps)
	if openCodeRunner == nil {
		return "", fmt.Errorf("no coding agent available")
	}

	if runnerWithContext, ok := openCodeRunner.(OpenCodeContextRunner); ok {
		openCodeErr = runnerWithContext.RunWithContext(stopCtx, leafID, opts.RepoRoot, prompt, opts.Model, opts.ConfigRoot, opts.ConfigDir, opts.LogPath)
	} else {
		openCodeErr = openCodeRunner.Run(leafID, opts.RepoRoot, prompt, opts.Model, opts.ConfigRoot, opts.ConfigDir, opts.LogPath)
	}
	if openCodeErr != nil {
		if cancelProgress != nil {
			cancelProgress()
		}
		if progress != nil {
			progress.Finish(openCodeErr)
		}
		var verificationErr *opencode.VerificationError
		if errors.As(openCodeErr, &verificationErr) {
			reason := "verification not confirmed"
			if verificationErr != nil && verificationErr.Reason != "" {
				reason = verificationErr.Reason
			}
			reason = sanitizeStallReason(reason)
			if err := beadsClient.UpdateStatusWithReason(leafID, "blocked", reason); err != nil {
				if err := beadsClient.UpdateStatus(leafID, "blocked"); err != nil {
					return "", err
				}
			}
			return "blocked", openCodeErr
		}
		if stall, ok := openCodeErr.(*opencode.StallError); ok {
			reason := sanitizeStallReason(stall.Error())
			if len(reason) > maxStallReasonLength {
				reason = reason[:maxStallReasonLength]
			}
			if err := beadsClient.UpdateStatusWithReason(leafID, "blocked", reason); err != nil {
				shortReason := fmt.Sprintf("opencode stall category=%s", stall.Category)
				if err := beadsClient.UpdateStatusWithReason(leafID, "blocked", shortReason); err != nil {
					if err := beadsClient.UpdateStatus(leafID, "blocked"); err != nil {
						return "", err
					}
				}
			}
			return "blocked", openCodeErr
		}
		if stopState.Requested() {
			if err := CleanupAfterStop(stopState, StopCleanupConfig{
				Beads:   beadsClient,
				Git:     cleanupGit{ctx: stopCtx, statusFn: opts.StatusPorcelain, restoreFn: opts.GitRestoreAll, cleanFn: opts.GitCleanAll},
				Out:     out,
				Confirm: opts.CleanupConfirm,
			}); err != nil {
				return "", err
			}
			return "stopped", openCodeErr
		}
		return "", openCodeErr
	}
	if cancelProgress != nil {
		cancelProgress()
	}
	if progress != nil {
		progress.Finish(nil)
	}
	if stopState.Requested() {
		if err := CleanupAfterStop(stopState, StopCleanupConfig{
			Beads:   beadsClient,
			Git:     cleanupGit{ctx: stopCtx, statusFn: opts.StatusPorcelain, restoreFn: opts.GitRestoreAll, cleanFn: opts.GitCleanAll},
			Out:     out,
			Confirm: opts.CleanupConfirm,
		}); err != nil {
			return "", err
		}
		return "stopped", context.Canceled
	}
	if progressCtx != nil && progressCtx.Err() == context.Canceled && stopCtx.Err() == context.Canceled {
		return "stopped", context.Canceled
	}

	emitPhase(deps.Events, EventOpenCodeEnd, leafID, bead.Title, currentProgress, opts.Model)

	emitPhase(deps.Events, EventGitAdd, leafID, bead.Title, currentProgress, "")
	if err := deps.Git.AddAll(); err != nil {
		return "", err
	}

	emitPhase(deps.Events, EventGitStatus, leafID, bead.Title, currentProgress, "")
	dirty, err := deps.Git.IsDirty()
	if err != nil {
		return "", err
	}

	if !dirty {
		commitSHA, err := deps.Git.RevParseHead()
		if err != nil {
			return "", err
		}
		if err := deps.Logger.AppendRunnerSummary(opts.RepoRoot, leafID, bead.Title, "blocked", commitSHA); err != nil {
			return "", err
		}
		if err := beadsClient.UpdateStatus(leafID, "blocked"); err != nil {
			return "", err
		}
		return "blocked", nil
	}

	commitMessage := "feat: complete bead task"
	if bead.Title != "" {
		commitMessage = "feat: " + strings.ToLower(bead.Title)
	}

	emitPhase(deps.Events, EventGitCommit, leafID, bead.Title, currentProgress, "")
	if err := deps.Git.Commit(commitMessage); err != nil {
		return "", err
	}

	commitSHA, err := deps.Git.RevParseHead()
	if err != nil {
		return "", err
	}
	if err := deps.Logger.AppendRunnerSummary(opts.RepoRoot, leafID, bead.Title, "completed", commitSHA); err != nil {
		return "", err
	}

	emitPhase(deps.Events, EventBeadsClose, leafID, bead.Title, currentProgress, "")
	if err := beadsClient.Close(leafID); err != nil {
		return "", err
	}

	emitPhase(deps.Events, EventBeadsVerify, leafID, bead.Title, currentProgress, "")
	closed, err := beadsClient.Show(leafID)
	if err != nil {
		return "", err
	}
	if closed.Status != "closed" {
		if err := deps.Logger.AppendRunnerSummary(opts.RepoRoot, leafID, bead.Title, "blocked", commitSHA); err != nil {
			return "", err
		}
		if err := beadsClient.UpdateStatus(leafID, "blocked"); err != nil {
			return "", err
		}
		return "blocked", nil
	}

	emitPhase(deps.Events, EventBeadsSync, leafID, bead.Title, currentProgress, "")
	if err := beadsClient.Sync(); err != nil {
		return "", err
	}

	elapsed := now().Sub(startTime).Round(time.Second)
	fmt.Fprintf(out, "Finished %s: completed (%ds)\n", leafID, int(elapsed.Seconds()))

	return "completed", nil
}

func emitPhase(emitter EventEmitter, eventType EventType, issueID string, title string, progress ProgressState, model string) {
	if emitter == nil {
		return
	}
	emitter.Emit(Event{
		Type:              eventType,
		IssueID:           issueID,
		Title:             title,
		Phase:             string(eventType),
		Model:             model,
		ProgressCompleted: progress.Completed,
		ProgressTotal:     progress.Total,
		EmittedAt:         time.Now(),
	})
}

func RunLoop(opts RunOnceOptions, deps RunOnceDeps, max int, runOnce func(RunOnceOptions, RunOnceDeps) (string, error)) (int, error) {
	if runOnce == nil {
		runOnce = RunOnce
	}

	completed := 0
	for {
		current := opts
		current.Progress.Completed = completed
		result, err := runOnce(current, deps)
		if err != nil {
			if result == "blocked" {
				completed++
				continue
			}
			return completed, err
		}

		switch result {
		case "completed":
			completed++
		case "blocked":
			completed++
			// Keep going; the next call should select a different open task.
		case "no_tasks":
			beadsClient := getLegacyBeadsClient(deps)
			if beadsClient != nil {
				if err := beadsClient.CloseEligible(); err != nil {
					return completed, err
				}
			}
			return completed, nil
		default:
			return completed, nil
		}

		if max > 0 && completed >= max {
			return completed, nil
		}
	}
}
