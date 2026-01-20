package runner

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"yolo-runner/internal/opencode"
	"yolo-runner/internal/ui"
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

type ContextOpenCodeRunner interface {
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
	Beads    BeadsClient
	Prompt   PromptBuilder
	OpenCode OpenCodeRunner
	Git      GitClient
	Logger   Logger
	Events   EventEmitter
}

type RunOnceOptions struct {
	RepoRoot       string
	RootID         string
	Model          string
	ConfigRoot     string
	ConfigDir      string
	LogPath        string
	DryRun         bool
	Out            io.Writer
	ProgressNow    func() time.Time
	ProgressTicker ui.ProgressTicker
	Progress       ProgressState
	Stop           *StopState
}

type ProgressState struct {
	Completed int
	Total     int
}

var now = time.Now

const maxStallReasonLength = 512

func RunOnce(opts RunOnceOptions, deps RunOnceDeps) (string, error) {
	out := opts.Out
	if out == nil {
		out = io.Discard
	}

	startTime := now()
	currentState := ""
	setState := func(state string) {
		if state == "" || state == currentState {
			return
		}
		currentState = state
		fmt.Fprintf(out, "State: %s\n", state)
	}

	root, err := deps.Beads.Ready(opts.RootID)
	if err != nil {
		return "", err
	}

	progressState := opts.Progress
	if progressState.Total == 0 {
		tree, err := deps.Beads.Tree(opts.RootID)
		if err != nil {
			return "", err
		}
		progressState.Total = CountRunnableLeaves(tree)
	}

	leafID := SelectFirstOpenLeafTaskID(root)
	if leafID == "" {
		fmt.Fprintln(out, "No tasks available")
		return "no_tasks", nil
	}

	bead, err := deps.Beads.Show(leafID)
	if err != nil {
		return "", err
	}

	fmt.Fprintf(out, "Starting [%d/%d] %s: %s\n", progressState.Completed, progressState.Total, leafID, bead.Title)
	setState("selecting task")

	emitPhase(deps.Events, EventSelectTask, leafID, bead.Title, progressState)

	prompt := deps.Prompt.Build(leafID, bead.Title, bead.Description, bead.AcceptanceCriteria)
	command := opencode.BuildArgs(opts.RepoRoot, prompt, opts.Model)

	if opts.DryRun {
		setState("dry run")
		fmt.Fprintf(out, "Task: %s - %s\n", leafID, bead.Title)
		fmt.Fprintln(out, prompt)
		fmt.Fprintf(out, "Command: %s\n", strings.Join(command, " "))
		elapsed := now().Sub(startTime).Round(time.Second)
		fmt.Fprintf(out, "Finished %s: dry_run (%s)\n", leafID, elapsed)
		return "dry_run", nil
	}

	setState("bd update")
	emitPhase(deps.Events, EventBeadsUpdate, leafID, bead.Title, progressState)
	if opts.Stop != nil {
		opts.Stop.MarkInProgress(leafID)
	}
	if err := deps.Beads.UpdateStatus(leafID, "in_progress"); err != nil {
		return "", err
	}

	setState("opencode running")
	emitPhase(deps.Events, EventOpenCodeStart, leafID, bead.Title, progressState)
	logPath := opts.LogPath

	if logPath == "" {
		logPath = filepath.Join(opts.RepoRoot, "runner-logs", "opencode", leafID+".jsonl")
	}
	progress := ui.NewProgress(ui.ProgressConfig{
		Writer:  out,
		State:   currentState,
		LogPath: logPath,
		Now:     opts.ProgressNow,
		Ticker:  opts.ProgressTicker,
	})
	progressCtx, cancelProgress := context.WithCancel(context.Background())
	go progress.Run(progressCtx)
	openCodeErr := runOpenCode(opts, deps, leafID, prompt, logPath)
	cancelProgress()
	if openCodeErr != nil {
		progress.Finish(openCodeErr)
		if openCodeErr == context.Canceled && opts.Stop != nil && opts.Stop.Requested() {
			if err := CleanupAfterStop(opts.Stop, StopCleanupConfig{
				Beads: deps.Beads,
			}); err != nil {
				return "", err
			}
			return "stopped", openCodeErr
		}
		if stall, ok := openCodeErr.(*opencode.StallError); ok {
			reason := sanitizeStallReason(stall.Error())
			if err := deps.Beads.UpdateStatusWithReason(leafID, "blocked", reason); err != nil {
				fallback := sanitizeStallReason("opencode stall category=" + stall.Category)
				if fallbackErr := deps.Beads.UpdateStatusWithReason(leafID, "blocked", fallback); fallbackErr != nil {
					return "", fallbackErr
				}
			}
			elapsed := now().Sub(startTime).Round(time.Second)
			fmt.Fprintf(out, "Finished %s: blocked (%s)\n", leafID, elapsed)
			return "blocked", openCodeErr
		}
		return "", openCodeErr
	}
	progress.Finish(nil)
	emitPhase(deps.Events, EventOpenCodeEnd, leafID, bead.Title, progressState)

	setState("git add")
	emitPhase(deps.Events, EventGitAdd, leafID, bead.Title, progressState)
	if err := deps.Git.AddAll(); err != nil {
		return "", err
	}

	setState("git status")
	emitPhase(deps.Events, EventGitStatus, leafID, bead.Title, progressState)
	dirty, err := deps.Git.IsDirty()
	if err != nil {
		return "", err
	}

	if !dirty {
		setState("no changes")
		commitSHA, err := deps.Git.RevParseHead()
		if err != nil {
			return "", err
		}
		if err := deps.Logger.AppendRunnerSummary(opts.RepoRoot, leafID, bead.Title, "blocked", commitSHA); err != nil {
			return "", err
		}
		if err := deps.Beads.UpdateStatus(leafID, "blocked"); err != nil {
			return "", err
		}
		elapsed := now().Sub(startTime).Round(time.Second)
		fmt.Fprintf(out, "Finished %s: blocked (%s)\n", leafID, elapsed)
		return "blocked", nil
	}

	commitMessage := "feat: complete bead task"
	if bead.Title != "" {
		commitMessage = "feat: " + strings.ToLower(bead.Title)
	}

	setState("git commit")
	emitPhase(deps.Events, EventGitCommit, leafID, bead.Title, progressState)
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

	setState("bd close")
	emitPhase(deps.Events, EventBeadsClose, leafID, bead.Title, progressState)
	if err := deps.Beads.Close(leafID); err != nil {
		return "", err
	}

	setState("bd verify")
	emitPhase(deps.Events, EventBeadsVerify, leafID, bead.Title, progressState)
	closed, err := deps.Beads.Show(leafID)
	if err != nil {
		return "", err
	}
	if closed.Status != "closed" {
		if err := deps.Logger.AppendRunnerSummary(opts.RepoRoot, leafID, bead.Title, "blocked", commitSHA); err != nil {
			return "", err
		}
		if err := deps.Beads.UpdateStatus(leafID, "blocked"); err != nil {
			return "", err
		}
		elapsed := now().Sub(startTime).Round(time.Second)
		fmt.Fprintf(out, "Finished %s: blocked (%s)\n", leafID, elapsed)
		return "blocked", nil
	}

	setState("bd sync")
	emitPhase(deps.Events, EventBeadsSync, leafID, bead.Title, progressState)
	if err := deps.Beads.Sync(); err != nil {
		return "", err
	}

	elapsed := now().Sub(startTime).Round(time.Second)
	fmt.Fprintf(out, "Finished %s: completed (%s)\n", leafID, elapsed)
	return "completed", nil
}

func emitPhase(emitter EventEmitter, eventType EventType, issueID string, title string, progress ProgressState) {
	if emitter == nil {
		return
	}
	emitter.Emit(Event{
		Type:              eventType,
		IssueID:           issueID,
		Title:             title,
		Phase:             string(eventType),
		ProgressCompleted: progress.Completed,
		ProgressTotal:     progress.Total,
		EmittedAt:         time.Now(),
	})
}

func sanitizeStallReason(reason string) string {
	if reason == "" {
		return ""
	}
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return ' '
		default:
			return r
		}
	}, reason)
	cleaned = strings.TrimSpace(cleaned)
	if len(cleaned) <= maxStallReasonLength {
		return cleaned
	}
	if maxStallReasonLength <= 3 {
		return cleaned[:maxStallReasonLength]
	}
	return cleaned[:maxStallReasonLength-3] + "..."
}

func runOpenCode(opts RunOnceOptions, deps RunOnceDeps, issueID string, prompt string, logPath string) error {
	if opts.Stop != nil {
		ctx := opts.Stop.Context()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if runner, ok := deps.OpenCode.(ContextOpenCodeRunner); ok {
			return runner.RunWithContext(ctx, issueID, opts.RepoRoot, prompt, opts.Model, opts.ConfigRoot, opts.ConfigDir, logPath)
		}
	}
	if deps.OpenCode == nil {
		return nil
	}
	return deps.OpenCode.Run(issueID, opts.RepoRoot, prompt, opts.Model, opts.ConfigRoot, opts.ConfigDir, logPath)
}

func RunLoop(opts RunOnceOptions, deps RunOnceDeps, max int, runOnce func(RunOnceOptions, RunOnceDeps) (string, error)) (int, error) {
	if runOnce == nil {
		runOnce = RunOnce
	}

	completed := 0
	for {
		opts.Progress.Completed = completed
		result, err := runOnce(opts, deps)
		if err != nil {
			// Allow the caller to keep going after a task is marked blocked.
			// This is primarily used for stall watchdog cases where we want to
			// continue with other tasks.
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
			if deps.Beads != nil {
				if err := deps.Beads.CloseEligible(); err != nil {
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
