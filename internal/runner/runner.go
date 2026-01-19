package runner

import (
	"fmt"
	"io"
	"strings"

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

	prompt := deps.Prompt.Build(leafID, bead.Title, bead.Description, bead.AcceptanceCriteria)
	command := opencode.BuildArgs(opts.RepoRoot, prompt, opts.Model)

	if opts.DryRun {
		fmt.Fprintf(out, "Task: %s - %s\n", leafID, bead.Title)
		fmt.Fprintln(out, prompt)
		fmt.Fprintf(out, "Command: %s\n", strings.Join(command, " "))
		return "dry_run", nil
	}

	if err := deps.Beads.UpdateStatus(leafID, "in_progress"); err != nil {
		return "", err
	}

	if err := deps.OpenCode.Run(leafID, opts.RepoRoot, prompt, opts.Model, opts.ConfigRoot, opts.ConfigDir, opts.LogPath); err != nil {
		return "", err
	}

	if err := deps.Git.AddAll(); err != nil {
		return "", err
	}

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

	if err := deps.Beads.Close(leafID); err != nil {
		return "", err
	}

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

	if err := deps.Beads.Sync(); err != nil {
		return "", err
	}

	return "completed", nil
}
