package main

import (
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
		},
		Env: []string{
			"YOLO_ARC_REVIEW_SESSION_ID=" + sessionID,
		},
		LogPath: filepath.Join(repoRoot, "runner-logs", "arc-pr-review-"+sanitizeArcReviewSessionID(sessionID)+".log"),
	}
}
