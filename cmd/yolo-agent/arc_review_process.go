package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const arcPRReviewRunnerBinary = "arc-pr-review-runner"

type arcReviewProcessConfig struct {
	RepoRoot   string
	Workspace  string
	PRID       string
	SessionID  string
	StatePath  string
	EventsPath string
}

type arcReviewProcessSpec struct {
	Argv    []string
	Env     []string
	LogPath string
}

type arcReviewStartedProcess struct {
	PID int
}

type arcReviewProcessStarter interface {
	StartArcReviewProcess(arcReviewProcessSpec) (arcReviewStartedProcess, error)
}

type arcReviewProcessStarterFunc func(arcReviewProcessSpec) (arcReviewStartedProcess, error)

func (f arcReviewProcessStarterFunc) StartArcReviewProcess(spec arcReviewProcessSpec) (arcReviewStartedProcess, error) {
	return f(spec)
}

var defaultArcReviewProcessStarter arcReviewProcessStarter = arcReviewProcessStarterFunc(startArcReviewProcess)

func buildArcReviewProcessSpec(cfg arcReviewProcessConfig) arcReviewProcessSpec {
	repoRoot := strings.TrimSpace(cfg.RepoRoot)
	sessionID := strings.TrimSpace(cfg.SessionID)
	return arcReviewProcessSpec{
		Argv: []string{
			arcPRReviewRunnerBinary,
			"--repo", repoRoot,
			"--workspace", strings.TrimSpace(cfg.Workspace),
			"--pr-id", strings.TrimSpace(cfg.PRID),
			"--state-path", strings.TrimSpace(cfg.StatePath),
			"--events", strings.TrimSpace(cfg.EventsPath),
			"--once",
		},
		Env:     nil,
		LogPath: filepath.Join(repoRoot, "runner-logs", "arc-pr-review-"+sanitizeArcReviewSessionID(sessionID)+".log"),
	}
}

func startArcReviewProcess(spec arcReviewProcessSpec) (arcReviewStartedProcess, error) {
	if len(spec.Argv) == 0 || strings.TrimSpace(spec.Argv[0]) == "" {
		return arcReviewStartedProcess{}, fmt.Errorf("arc review process argv is required")
	}
	if err := os.MkdirAll(filepath.Dir(spec.LogPath), 0o755); err != nil {
		return arcReviewStartedProcess{}, fmt.Errorf("create arc review process log directory: %w", err)
	}
	logFile, err := os.OpenFile(spec.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return arcReviewStartedProcess{}, fmt.Errorf("open arc review process log %q: %w", spec.LogPath, err)
	}
	defer logFile.Close()

	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Env = append(os.Environ(), spec.Env...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return arcReviewStartedProcess{}, fmt.Errorf("start arc review process: %w", err)
	}
	return arcReviewStartedProcess{PID: cmd.Process.Pid}, nil
}
