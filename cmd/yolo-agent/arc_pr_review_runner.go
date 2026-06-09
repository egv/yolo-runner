package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	arcreviewstate "github.com/egv/yolo-runner/v2/internal/arcreview/state"
)

type arcPRReviewRunnerCommandConfig struct {
	repoRoot   string
	workspace  string
	prID       string
	statePath  string
	eventsPath string
	once       bool
}

var runArcPRReviewRunner = defaultRunArcPRReviewRunner

func arcPRReviewRunnerCommand(args []string) int {
	fs := flag.NewFlagSet("arc-pr-review-runner", flag.ContinueOnError)
	repo := fs.String("repo", ".", "Repository root")
	workspace := fs.String("workspace", "", "Arc PR workspace")
	pr := fs.String("pr", "", "Arc PR ID")
	prIDAlias := fs.String("pr-id", "", "Arc PR ID")
	state := fs.String("state", "", "Arc review state DB path")
	statePathAlias := fs.String("state-path", "", "Arc review state DB path")
	events := fs.String("events", "", "Path to JSONL events log")
	once := fs.Bool("once", false, "Write one heartbeat and exit")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected arc-pr-review-runner argument: %s\n", fs.Arg(0))
		return 1
	}

	prID := strings.TrimSpace(*pr)
	if prID == "" {
		prID = strings.TrimSpace(*prIDAlias)
	}
	statePath := strings.TrimSpace(*state)
	if statePath == "" {
		statePath = strings.TrimSpace(*statePathAlias)
	}

	if err := runArcPRReviewRunner(context.Background(), arcPRReviewRunnerCommandConfig{
		repoRoot:   strings.TrimSpace(*repo),
		workspace:  strings.TrimSpace(*workspace),
		prID:       prID,
		statePath:  statePath,
		eventsPath: strings.TrimSpace(*events),
		once:       *once,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func defaultRunArcPRReviewRunner(_ context.Context, cfg arcPRReviewRunnerCommandConfig) error {
	if strings.TrimSpace(cfg.repoRoot) == "" {
		return fmt.Errorf("--repo is required")
	}
	if strings.TrimSpace(cfg.workspace) == "" {
		return fmt.Errorf("--workspace is required")
	}
	if strings.TrimSpace(cfg.prID) == "" {
		return fmt.Errorf("--pr is required")
	}
	if strings.TrimSpace(cfg.statePath) == "" {
		return fmt.Errorf("--state is required")
	}
	if !cfg.once {
		return fmt.Errorf("--once is required until continuous PR review runner mode is implemented")
	}

	store, err := arcreviewstate.Open(cfg.statePath)
	if err != nil {
		return err
	}
	defer store.Close()

	session, err := resolveArcPRReviewRunnerSession(store, cfg.prID, cfg.workspace)
	if err != nil {
		return err
	}
	if err := store.UpdateHeartbeat(session.ID, time.Now().UTC()); err != nil {
		return err
	}
	return nil
}

func resolveArcPRReviewRunnerSession(store *arcreviewstate.Store, prID string, workspace string) (arcreviewstate.Session, error) {
	sessions, err := store.ListSessionsByPRID(prID)
	if err != nil {
		return arcreviewstate.Session{}, err
	}
	workspace = strings.TrimSpace(workspace)
	var matches []arcreviewstate.Session
	for _, session := range sessions {
		if strings.TrimSpace(session.Workspace) == workspace {
			matches = append(matches, session)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return arcreviewstate.Session{}, fmt.Errorf("no PR review session found for pr %q workspace %q", strings.TrimSpace(prID), workspace)
	}
	return arcreviewstate.Session{}, fmt.Errorf("multiple PR review sessions found for pr %q workspace %q", strings.TrimSpace(prID), workspace)
}
