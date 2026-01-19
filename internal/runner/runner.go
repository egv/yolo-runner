package runner

import (
	"fmt"
	"io"
	"strings"
	"time"

	"yolo-runner/internal/opencode"
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
	Show(id string) (Bead, error)
	UpdateStatus(id string, status string) error
	UpdateStatusWithReason(id string, status string, reason string) error
	Close(id string) error
	Sync() error
}

type PromptBuilder interface {
	Build(issueID string, title string, description string, acceptance string) string
}

type OpenCodeRunner interface {
	Run(issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string) error
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
	RepoRoot   string
	RootID     string
	Model      string
	ConfigRoot string
	ConfigDir  string
	LogPath    string
	DryRun     bool
	Out        io.Writer
}

func RunOnce(opts RunOnceOptions, deps RunOnceDeps) (string, error) {
	out := opts.Out
	if out == nil {
		out = io.Discard
	}

	root, err := deps.Beads.Ready(opts.RootID)
	if err != nil {
		return "", err
	}

	leafID := SelectFirstOpenLeafTaskID(root)
	if leafID == "" {
		return "no_tasks", nil
	}

	bead, err := deps.Beads.Show(leafID)
	if err != nil {
		return "", err
	}

	emitPhase(deps.Events, EventSelectTask, leafID, bead.Title)

	prompt := deps.Prompt.Build(leafID, bead.Title, bead.Description, bead.AcceptanceCriteria)
	command := opencode.BuildArgs(opts.RepoRoot, prompt, opts.Model)

	if opts.DryRun {
		fmt.Fprintf(out, "Task: %s - %s\n", leafID, bead.Title)
		fmt.Fprintln(out, prompt)
		fmt.Fprintf(out, "Command: %s\n", strings.Join(command, " "))
		return "dry_run", nil
	}

	emitPhase(deps.Events, EventBeadsUpdate, leafID, bead.Title)
	if err := deps.Beads.UpdateStatus(leafID, "in_progress"); err != nil {
		return "", err
	}

	emitPhase(deps.Events, EventOpenCodeStart, leafID, bead.Title)
	if err := deps.OpenCode.Run(leafID, opts.RepoRoot, prompt, opts.Model, opts.ConfigRoot, opts.ConfigDir, opts.LogPath); err != nil {
		if stall, ok := err.(*opencode.StallError); ok {
			reason := stall.Error()
			if err := deps.Beads.UpdateStatusWithReason(leafID, "blocked", reason); err != nil {
				return "", err
			}
			return "blocked", err
		}
		return "", err
	}
	emitPhase(deps.Events, EventOpenCodeEnd, leafID, bead.Title)

	emitPhase(deps.Events, EventGitAdd, leafID, bead.Title)
	if err := deps.Git.AddAll(); err != nil {
		return "", err
	}

	emitPhase(deps.Events, EventGitStatus, leafID, bead.Title)
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
		if err := deps.Beads.UpdateStatus(leafID, "blocked"); err != nil {
			return "", err
		}
		return "blocked", nil
	}

	commitMessage := "feat: complete bead task"
	if bead.Title != "" {
		commitMessage = "feat: " + strings.ToLower(bead.Title)
	}

	emitPhase(deps.Events, EventGitCommit, leafID, bead.Title)
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

	emitPhase(deps.Events, EventBeadsClose, leafID, bead.Title)
	if err := deps.Beads.Close(leafID); err != nil {
		return "", err
	}

	emitPhase(deps.Events, EventBeadsVerify, leafID, bead.Title)
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
		return "blocked", nil
	}

	emitPhase(deps.Events, EventBeadsSync, leafID, bead.Title)
	if err := deps.Beads.Sync(); err != nil {
		return "", err
	}

	return "completed", nil
}

func emitPhase(emitter EventEmitter, eventType EventType, issueID string, title string) {
	if emitter == nil {
		return
	}
	emitter.Emit(Event{
		Type:      eventType,
		IssueID:   issueID,
		Title:     title,
		Phase:     string(eventType),
		EmittedAt: time.Now(),
	})
}

func RunLoop(opts RunOnceOptions, deps RunOnceDeps, max int, runOnce func(RunOnceOptions, RunOnceDeps) (string, error)) (int, error) {
	if runOnce == nil {
		runOnce = RunOnce
	}

	completed := 0
	for {
		result, err := runOnce(opts, deps)
		if err != nil {
			return completed, err
		}
		if result == "completed" {
			completed++
		}
		if result == "no_tasks" {
			return completed, nil
		}
		if max > 0 && completed >= max {
			return completed, nil
		}
		if result != "completed" {
			return completed, nil
		}
	}
}
