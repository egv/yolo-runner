package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/egv/yolo-runner/v2/internal/arcanum"
	arcreviewstate "github.com/egv/yolo-runner/v2/internal/arcreview/state"
	"github.com/egv/yolo-runner/v2/internal/contracts"
)

const arcReviewPendingSessionStatus = arcreviewstate.SessionStatusPending

type arcReviewDiscoveredPR struct {
	ID        string
	Workspace string
	Branch    string
	Revision  string
}

var discoverArcReviewPRs = defaultDiscoverArcReviewPRs
var listArcReviewWorkspacePRs = arcanum.ListWorkspacePRs
var defaultArcReviewPIDLiveness arcReviewPIDLivenessChecker = arcReviewPIDLivenessFunc(isArcReviewPIDAlive)
var arcReviewWatchPollWait = waitTrackerWatchPollInterval
var newArcReviewWatchConfigService = func() arcReviewWatchConfigResolver {
	return newTrackerConfigService()
}

type arcReviewPIDLivenessChecker interface {
	IsArcReviewPIDAlive(pid int) bool
}

type arcReviewWatchConfigResolver interface {
	ResolveArcReviewWatchConfig(repoRoot string) (arcReviewWatchConfig, error)
}

type arcReviewPIDLivenessFunc func(pid int) bool

func (f arcReviewPIDLivenessFunc) IsArcReviewPIDAlive(pid int) bool {
	return f(pid)
}

func defaultRunArcReviewWatch(ctx context.Context, cfg arcReviewWatchCommandConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg.eventsPath = resolveArcReviewWatchEventsPath(cfg)
	eventSink, closeEventSink := arcReviewWatchEventSink(cfg)
	defer closeEventSink()
	cfg.eventSink = eventSink

	configService := newArcReviewWatchConfigService()
	reviewWatchConfig, err := configService.ResolveArcReviewWatchConfig(cfg.repoRoot)
	if err != nil {
		return err
	}
	lock, err := acquireTrackerWatchLock(reviewWatchConfig.LockPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = lock.Release()
	}()

	emitArcReviewWatchStarted(ctx, cfg, reviewWatchConfig)
	pollInterval := arcReviewWatchDynamicPollIntervalProvider(cfg.repoRoot, configService, reviewWatchConfig.PollInterval)
	err = runResilientWatchPollLoop(ctx, cfg.once, pollInterval, func(context.Context) error {
		return runArcReviewWatchPollIterationWithConfigService(cfg, configService)
	}, cfg.eventSink, arcReviewWatchPollWait)
	emitArcReviewWatchFinished(ctx, cfg, reviewWatchConfig, err)
	return err
}

func resolveArcReviewWatchEventsPath(cfg arcReviewWatchCommandConfig) string {
	return resolveWatchEventsPath(cfg.eventsPath, cfg.stream, cfg.repoRoot, "arc-review-watch.events.jsonl")
}

func arcReviewWatchEventSink(cfg arcReviewWatchCommandConfig) (contracts.EventSink, func()) {
	return watchEventSink(cfg.stream, cfg.eventsPath)
}

func arcReviewWatchDynamicPollIntervalProvider(repoRoot string, configService arcReviewWatchConfigResolver, lastGood time.Duration) trackerWatchPollIntervalProvider {
	var resolve func() (time.Duration, error)
	if configService != nil {
		resolve = func() (time.Duration, error) {
			reviewWatchConfig, err := configService.ResolveArcReviewWatchConfig(repoRoot)
			if err != nil {
				return 0, err
			}
			return reviewWatchConfig.PollInterval, nil
		}
	}
	return dynamicWatchPollIntervalProvider(lastGood, resolve)
}

func runArcReviewWatchPollIteration(cfg arcReviewWatchCommandConfig) error {
	return runArcReviewWatchPollIterationWithConfigService(cfg, newArcReviewWatchConfigService())
}

func runArcReviewWatchPollIterationWithConfigService(cfg arcReviewWatchCommandConfig, configService arcReviewWatchConfigResolver) error {
	if configService == nil {
		return errors.New("arc-review-watch config service is required")
	}
	reviewWatchConfig, err := configService.ResolveArcReviewWatchConfig(cfg.repoRoot)
	if err != nil {
		return err
	}
	prs, err := discoverArcReviewPRs(cfg, reviewWatchConfig)
	if err != nil {
		return err
	}
	if cfg.dryRun {
		return nil
	}

	store, err := arcreviewstate.Open(reviewWatchConfig.StatePath)
	if err != nil {
		return err
	}
	defer func() {
		_ = store.Close()
	}()
	_, err = reconcileArcReviewSessions(store, prs)
	if err != nil {
		return err
	}
	_, err = restartStaleArcReviewSessions(store, cfg, reviewWatchConfig, time.Now().UTC(), defaultArcReviewPIDLiveness, defaultArcReviewProcessStarter)
	return err
}

func defaultDiscoverArcReviewPRs(_ arcReviewWatchCommandConfig, cfg arcReviewWatchConfig) ([]arcReviewDiscoveredPR, error) {
	seen := map[string]struct{}{}
	var discovered []arcReviewDiscoveredPR
	for _, workspace := range cfg.Workspaces {
		prs, err := listArcReviewWorkspacePRs(context.Background(), workspace)
		if err != nil {
			return nil, fmt.Errorf("list arc review PRs in workspace %q: %w", workspace, err)
		}
		for _, pr := range mapEligiblePRSummariesToArcReviewDiscoveredPRs(workspace, cfg.Reviewer, cfg.Branches, prs) {
			pr.ID = strings.TrimSpace(pr.ID)
			if _, ok := seen[pr.ID]; ok {
				continue
			}
			seen[pr.ID] = struct{}{}
			discovered = append(discovered, pr)
		}
	}
	return discovered, nil
}

func reconcileArcReviewSessions(store *arcreviewstate.Store, prs []arcReviewDiscoveredPR) ([]arcreviewstate.Session, error) {
	if store == nil {
		return nil, errors.New("arc review state store is required")
	}

	var created []arcreviewstate.Session
	for _, pr := range prs {
		pr.ID = strings.TrimSpace(pr.ID)
		if pr.ID == "" {
			return nil, errors.New("PR ID is required")
		}
		sessions, err := store.ListSessionsByPRID(pr.ID)
		if err != nil {
			return nil, err
		}
		if hasNonTerminalArcReviewSession(sessions) {
			continue
		}

		session, err := store.CreateSession(arcreviewstate.Session{
			ID:        arcReviewSessionID(pr.ID, sessions),
			PRID:      pr.ID,
			Workspace: strings.TrimSpace(pr.Workspace),
			Branch:    strings.TrimSpace(pr.Branch),
			Status:    arcReviewPendingSessionStatus,
			Revision:  strings.TrimSpace(pr.Revision),
		})
		if err != nil {
			return nil, err
		}
		created = append(created, session)
	}
	return created, nil
}

func hasNonTerminalArcReviewSession(sessions []arcreviewstate.Session) bool {
	for _, session := range sessions {
		if !isTerminalArcReviewSessionStatus(session.Status) {
			return true
		}
	}
	return false
}

func isTerminalArcReviewSessionStatus(status string) bool {
	return arcreviewstate.IsTerminalSessionStatus(status)
}

func restartStaleArcReviewSessions(
	store *arcreviewstate.Store,
	commandCfg arcReviewWatchCommandConfig,
	watchCfg arcReviewWatchConfig,
	checkedAt time.Time,
	liveness arcReviewPIDLivenessChecker,
	starter arcReviewProcessStarter,
) (int, error) {
	if store == nil {
		return 0, errors.New("arc review state store is required")
	}
	if liveness == nil {
		liveness = defaultArcReviewPIDLiveness
	}
	if starter == nil {
		starter = defaultArcReviewProcessStarter
	}
	sessions, err := store.ListSessions()
	if err != nil {
		return 0, err
	}

	restarted := 0
	for _, session := range sessions {
		if !shouldRestartArcReviewSession(session, checkedAt, watchCfg.PollInterval, liveness) {
			continue
		}
		failed := session
		failed.Status = arcreviewstate.SessionStatusCrashed
		failed.PID = 0
		failed.FailureCount++
		if _, err := store.UpdateSession(failed); err != nil {
			return restarted, err
		}

		allForPR, err := store.ListSessionsByPRID(failed.PRID)
		if err != nil {
			return restarted, err
		}
		replacement := arcreviewstate.Session{
			ID:           arcReviewSessionID(failed.PRID, allForPR),
			PRID:         failed.PRID,
			Workspace:    failed.Workspace,
			Branch:       failed.Branch,
			Status:       arcreviewstate.SessionStatusRunning,
			Revision:     failed.Revision,
			Heartbeat:    checkedAt,
			FailureCount: failed.FailureCount,
		}
		spec := buildArcReviewProcessSpec(arcReviewProcessConfig{
			RepoRoot:   commandCfg.repoRoot,
			Workspace:  replacement.Workspace,
			PRID:       replacement.PRID,
			SessionID:  replacement.ID,
			StatePath:  watchCfg.StatePath,
			EventsPath: resolveArcReviewWatchEventsPath(commandCfg),
			AllowShip:  watchCfg.AllowShip,
			Reviewer:   watchCfg.Reviewer,
		})
		replacement.LogPath = spec.LogPath
		if _, err := store.CreateSession(replacement); err != nil {
			return restarted, err
		}
		started, err := starter.StartArcReviewProcess(spec)
		if err != nil {
			replacement.Status = arcreviewstate.SessionStatusCrashed
			if _, updateErr := store.UpdateSession(replacement); updateErr != nil {
				return restarted, fmt.Errorf("%w; also failed to mark replacement session crashed: %v", err, updateErr)
			}
			return restarted, err
		}
		replacement.PID = started.PID
		if _, err := store.UpdateSession(replacement); err != nil {
			return restarted, err
		}
		restarted++
	}
	return restarted, nil
}

func shouldRestartArcReviewSession(session arcreviewstate.Session, checkedAt time.Time, maxHeartbeatAge time.Duration, liveness arcReviewPIDLivenessChecker) bool {
	if !strings.EqualFold(strings.TrimSpace(session.Status), arcreviewstate.SessionStatusRunning) {
		return false
	}
	if arcreviewstate.HeartbeatFreshness(session.Heartbeat, checkedAt, maxHeartbeatAge).Stale {
		return true
	}
	return session.PID <= 0 || !liveness.IsArcReviewPIDAlive(session.PID)
}

func isArcReviewPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil || process == nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

func arcReviewSessionID(prID string, existing []arcreviewstate.Session) string {
	base := fmt.Sprintf("pr-%s", sanitizeArcReviewSessionID(prID))
	if len(existing) == 0 {
		return base
	}
	return fmt.Sprintf("%s-%d", base, len(existing)+1)
}

func sanitizeArcReviewSessionID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastWasDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastWasDash = false
			continue
		}
		if !lastWasDash && b.Len() > 0 {
			b.WriteByte('-')
			lastWasDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func emitArcReviewWatchStarted(ctx context.Context, cfg arcReviewWatchCommandConfig, reviewWatchConfig arcReviewWatchConfig) {
	emitArcReviewWatchEvent(ctx, cfg.eventSink, contracts.Event{
		Type:      contracts.EventTypeRunStarted,
		TaskTitle: "arc-review-watch",
		Metadata:  arcReviewWatchEventMetadata(cfg, reviewWatchConfig, nil),
		Timestamp: time.Now().UTC(),
	})
}

func emitArcReviewWatchFinished(ctx context.Context, cfg arcReviewWatchCommandConfig, reviewWatchConfig arcReviewWatchConfig, runErr error) {
	emitArcReviewWatchEvent(ctx, cfg.eventSink, contracts.Event{
		Type:      contracts.EventTypeRunFinished,
		TaskTitle: "arc-review-watch",
		Metadata:  arcReviewWatchEventMetadata(cfg, reviewWatchConfig, runErr),
		Timestamp: time.Now().UTC(),
	})
}

func emitArcReviewWatchEvent(ctx context.Context, sink contracts.EventSink, event contracts.Event) {
	if sink == nil {
		return
	}
	_ = sink.Emit(ctx, event)
}

func arcReviewWatchEventMetadata(cfg arcReviewWatchCommandConfig, reviewWatchConfig arcReviewWatchConfig, runErr error) map[string]string {
	metadata := map[string]string{
		"command":         "arc-review-watch",
		"repo":            strings.TrimSpace(cfg.repoRoot),
		"profile":         strings.TrimSpace(cfg.profile),
		"dry_run":         strconv.FormatBool(cfg.dryRun),
		"once":            strconv.FormatBool(cfg.once),
		"poll_interval":   reviewWatchConfig.PollInterval.String(),
		"lock_path":       strings.TrimSpace(reviewWatchConfig.LockPath),
		"state_path":      strings.TrimSpace(reviewWatchConfig.StatePath),
		"max_concurrency": strconv.Itoa(reviewWatchConfig.MaxConcurrency),
		"allow_ship":      strconv.FormatBool(reviewWatchConfig.AllowShip),
	}
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		metadata["error"] = runErr.Error()
	}
	return compactTrackerWatchMetadata(metadata)
}
