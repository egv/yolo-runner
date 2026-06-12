package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/egv/yolo-runner/v2/internal/arcanum"
	"github.com/egv/yolo-runner/v2/internal/arcreview"
	arcreviewstate "github.com/egv/yolo-runner/v2/internal/arcreview/state"
)

type arcPRReviewRunnerCommandConfig struct {
	repoRoot   string
	workspace  string
	prID       string
	sessionID  string
	statePath  string
	eventsPath string
	allowShip  bool
	reviewer   string
	once       bool
}

type arcPRReviewRunnerCycleResult struct {
	Action   arcreview.PRRunnerAction
	Terminal bool
}

type arcPRReviewRunnerCycleFunc func(context.Context, arcPRReviewCycleConfig) (arcPRReviewRunnerCycleResult, error)
type arcPRReviewRunnerHeartbeatFunc func(context.Context, time.Time) error
type arcPRReviewRunnerClockFunc func() time.Time
type arcPRReviewRunnerWaitFunc func(context.Context, time.Duration) error

type arcPRReviewRunnerReviewWatchConfigResolver interface {
	ResolveArcReviewWatchConfig(repoRoot string) (arcReviewWatchConfig, error)
}

type arcPRReviewRunnerLoopConfig struct {
	CycleConfig               arcPRReviewCycleConfig
	PollInterval              time.Duration
	ReviewWatchConfigResolver arcPRReviewRunnerReviewWatchConfigResolver
	Heartbeat                 arcPRReviewRunnerHeartbeatFunc
	Cycle                     arcPRReviewRunnerCycleFunc
	Now                       arcPRReviewRunnerClockFunc
	Wait                      arcPRReviewRunnerWaitFunc
}

var runArcPRReviewRunner = defaultRunArcPRReviewRunner

// firstNonEmptyFlag returns the first flag value that is non-empty after
// trimming, supporting aliased flag spellings.
func firstNonEmptyFlag(values ...*string) string {
	for _, value := range values {
		if value == nil {
			continue
		}
		if trimmed := strings.TrimSpace(*value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func arcPRReviewRunnerCommand(args []string) int {
	fs := flag.NewFlagSet("arc-pr-review-runner", flag.ContinueOnError)
	repo := fs.String("repo", ".", "Repository root")
	workspace := fs.String("workspace", "", "Arc PR workspace")
	pr := fs.String("pr", "", "Arc PR ID")
	prIDAlias := fs.String("pr-id", "", "Arc PR ID")
	session := fs.String("session", "", "Arc review session ID")
	sessionIDAlias := fs.String("session-id", "", "Arc review session ID")
	state := fs.String("state", "", "Arc review state DB path")
	statePathAlias := fs.String("state-path", "", "Arc review state DB path")
	events := fs.String("events", "", "Path to JSONL events log")
	allowShip := fs.Bool("allow-ship", false, "Allow the Arc PR review runner to ship when the gate passes")
	reviewer := fs.String("reviewer", "", "Arc reviewer identity")
	once := fs.Bool("once", false, "Write one heartbeat and exit")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected arc-pr-review-runner argument: %s\n", fs.Arg(0))
		return 1
	}

	prID := firstNonEmptyFlag(pr, prIDAlias)
	statePath := firstNonEmptyFlag(state, statePathAlias)
	sessionID := firstNonEmptyFlag(session, sessionIDAlias)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runArcPRReviewRunner(ctx, arcPRReviewRunnerCommandConfig{
		repoRoot:   strings.TrimSpace(*repo),
		workspace:  strings.TrimSpace(*workspace),
		prID:       prID,
		sessionID:  sessionID,
		statePath:  statePath,
		eventsPath: strings.TrimSpace(*events),
		allowShip:  *allowShip,
		reviewer:   strings.TrimSpace(*reviewer),
		once:       *once,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func defaultRunArcPRReviewRunner(ctx context.Context, cfg arcPRReviewRunnerCommandConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
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

	store, err := arcreviewstate.Open(cfg.statePath)
	if err != nil {
		return err
	}
	defer store.Close()

	session, err := resolveArcPRReviewRunnerSession(store, cfg.prID, cfg.workspace, cfg.sessionID)
	if err != nil {
		return err
	}
	if cfg.once {
		if err := store.UpdateHeartbeat(session.ID, time.Now().UTC()); err != nil {
			return err
		}
		return nil
	}

	configService := newTrackerConfigService()
	reviewWatchConfig, err := configService.ResolveArcReviewWatchConfig(cfg.repoRoot)
	if err != nil {
		return err
	}
	runner, runnerDefaults, err := buildTrackerWatchRunner(cfg.repoRoot)
	if err != nil {
		return err
	}
	apiClient, err := arcanum.NewAPIClient(arcanum.APIClientConfig{})
	if err != nil {
		return err
	}
	metadata := map[string]string{"phase": "arc_pr_review_cycle"}
	if reviewer := strings.TrimSpace(cfg.reviewer); reviewer != "" {
		metadata["reviewer"] = reviewer
	}

	return runArcPRReviewRunnerLoop(ctx, arcPRReviewRunnerLoopConfig{
		CycleConfig: arcPRReviewCycleConfig{
			PRID:          cfg.prID,
			Workspace:     cfg.workspace,
			RepoRoot:      cfg.repoRoot,
			Model:         runnerDefaults.Config.Model,
			Timeout:       runnerDefaults.RunnerTimeoutValue(),
			MaxRetries:    runnerDefaults.RetryBudgetValue(),
			Metadata:      metadata,
			AllowShip:     cfg.allowShip,
			StateFetcher:  arcPRReviewCycleStateFetcherFunc(arcanum.FetchPRRuntimeState),
			RevisionStore: store,
			ModelHelper: arcPRReviewCycleModelHelperFunc(func(ctx context.Context, input arcPRReviewModelInput) ([]byte, error) {
				return runArcPRReviewModel(ctx, runner, input)
			}),
			ReviewApplier: arcreview.ReviewApplier{
				Client: arcanum.NewReviewArcanumClient(apiClient),
				Store:  store,
			},
			ReplyApplier: arcreview.ReplyApplier{
				Client: arcanum.NewReplyArcanumClient(apiClient),
				Store:  store,
			},
			ShipGate: arcreview.ShipGate{
				Client: arcanum.NewShipArcanumClient(cfg.workspace),
			},
		},
		PollInterval:              reviewWatchConfig.PollInterval,
		ReviewWatchConfigResolver: configService,
		Heartbeat: func(_ context.Context, at time.Time) error {
			return store.UpdateHeartbeat(session.ID, at)
		},
		Cycle: defaultRunArcPRReviewRunnerCycle,
		Now: func() time.Time {
			return time.Now().UTC()
		},
		Wait: waitTrackerWatchPollInterval,
	})
}

type arcPRReviewCycleStateFetcherFunc func(context.Context, string, string) (arcreview.PRRuntimeState, error)

func (f arcPRReviewCycleStateFetcherFunc) FetchPRRuntimeState(ctx context.Context, workspace string, prID string) (arcreview.PRRuntimeState, error) {
	return f(ctx, workspace, prID)
}

func runArcPRReviewRunnerLoop(ctx context.Context, cfg arcPRReviewRunnerLoopConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if cfg.PollInterval <= 0 {
		return errors.New("arc PR review runner poll interval must be greater than 0")
	}
	if cfg.Heartbeat == nil {
		return errors.New("arc PR review runner heartbeat is required")
	}
	if cfg.Cycle == nil {
		return errors.New("arc PR review runner cycle is required")
	}
	if cfg.Now == nil {
		return errors.New("arc PR review runner clock is required")
	}
	if cfg.Wait == nil {
		return errors.New("arc PR review runner wait is required")
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		if err := cfg.Heartbeat(ctx, cfg.Now().UTC()); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		cycleConfig := cfg.CycleConfig
		if cfg.ReviewWatchConfigResolver != nil {
			if reviewWatchConfig, err := cfg.ReviewWatchConfigResolver.ResolveArcReviewWatchConfig(cfg.CycleConfig.RepoRoot); err == nil {
				cycleConfig = arcPRReviewRunnerCycleConfigWithReviewWatchConfig(cfg.CycleConfig, reviewWatchConfig)
			}
		}
		result, err := cfg.Cycle(ctx, cycleConfig)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		if result.Terminal {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return nil
		}
		if err := cfg.Wait(ctx, cfg.PollInterval); err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
	}
}

func arcPRReviewRunnerCycleConfigWithReviewWatchConfig(base arcPRReviewCycleConfig, reviewWatchConfig arcReviewWatchConfig) arcPRReviewCycleConfig {
	cycleConfig := base
	cycleConfig.AllowShip = reviewWatchConfig.AllowShip
	cycleConfig.Metadata = arcPRReviewRunnerMetadataWithReviewer(base.Metadata, reviewWatchConfig.Reviewer)
	return cycleConfig
}

func arcPRReviewRunnerMetadataWithReviewer(metadata map[string]string, reviewer string) map[string]string {
	out := cloneArcPRReviewModelMetadata(metadata)
	reviewer = strings.TrimSpace(reviewer)
	if reviewer == "" {
		delete(out, "reviewer")
		if len(out) == 0 {
			return nil
		}
		return out
	}
	if out == nil {
		out = map[string]string{}
	}
	out["reviewer"] = reviewer
	return out
}

func defaultRunArcPRReviewRunnerCycle(ctx context.Context, cfg arcPRReviewCycleConfig) (arcPRReviewRunnerCycleResult, error) {
	recorder := &arcPRReviewRunnerRecordingFetcher{inner: cfg.StateFetcher}
	cfg.StateFetcher = recorder
	action, err := runArcPRReviewCycle(ctx, cfg)
	return arcPRReviewRunnerCycleResult{
		Action:   action,
		Terminal: recorder.fetched && isTerminalArcPRReviewRunnerStatus(recorder.state.Details.Status),
	}, err
}

type arcPRReviewRunnerRecordingFetcher struct {
	inner   arcPRReviewCycleStateFetcher
	state   arcreview.PRRuntimeState
	fetched bool
}

func (f *arcPRReviewRunnerRecordingFetcher) FetchPRRuntimeState(ctx context.Context, workspace string, prID string) (arcreview.PRRuntimeState, error) {
	if f.inner == nil {
		return arcreview.PRRuntimeState{}, errors.New("arc PR review state fetcher is required")
	}
	state, err := f.inner.FetchPRRuntimeState(ctx, workspace, prID)
	if err != nil {
		return state, err
	}
	f.state = state
	f.fetched = true
	return state, nil
}

func isTerminalArcPRReviewRunnerStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "abandoned", "cancelled", "canceled", "closed", "completed", "declined", "landed", "merged", "rejected":
		return true
	default:
		return false
	}
}

func resolveArcPRReviewRunnerSession(store *arcreviewstate.Store, prID string, workspace string, sessionID string) (arcreviewstate.Session, error) {
	prID = strings.TrimSpace(prID)
	workspace = strings.TrimSpace(workspace)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID != "" {
		session, err := store.GetSession(sessionID)
		if err != nil {
			return arcreviewstate.Session{}, fmt.Errorf("get PR review session %q: %w", sessionID, err)
		}
		if strings.TrimSpace(session.PRID) != prID {
			return arcreviewstate.Session{}, fmt.Errorf("PR review session %q belongs to pr %q, not %q", sessionID, strings.TrimSpace(session.PRID), prID)
		}
		if strings.TrimSpace(session.Workspace) != workspace {
			return arcreviewstate.Session{}, fmt.Errorf("PR review session %q uses workspace %q, not %q", sessionID, strings.TrimSpace(session.Workspace), workspace)
		}
		return session, nil
	}

	sessions, err := store.ListSessionsByPRID(prID)
	if err != nil {
		return arcreviewstate.Session{}, err
	}
	var matches []arcreviewstate.Session
	for _, session := range sessions {
		if strings.TrimSpace(session.Workspace) == workspace {
			matches = append(matches, session)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	var running []arcreviewstate.Session
	for _, session := range matches {
		if strings.EqualFold(strings.TrimSpace(session.Status), arcreviewstate.SessionStatusRunning) {
			running = append(running, session)
		}
	}
	if len(running) == 1 {
		return running[0], nil
	}
	if len(matches) == 0 {
		return arcreviewstate.Session{}, fmt.Errorf("no PR review session found for pr %q workspace %q", prID, workspace)
	}
	return arcreviewstate.Session{}, fmt.Errorf("multiple PR review sessions found for pr %q workspace %q", prID, workspace)
}
