package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	arcreviewstate "github.com/egv/yolo-runner/v2/internal/arcreview/state"
	"github.com/egv/yolo-runner/v2/internal/contracts"
)

const arcReviewPendingSessionStatus = "pending"

type arcReviewDiscoveredPR struct {
	ID        string
	Workspace string
	Branch    string
	Revision  string
}

var discoverArcReviewPRs = defaultDiscoverArcReviewPRs
var defaultArcReviewPIDLiveness arcReviewPIDLivenessChecker = arcReviewPIDLivenessFunc(isArcReviewPIDAlive)

type arcReviewPIDLivenessChecker interface {
	IsArcReviewPIDAlive(pid int) bool
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

	reviewWatchConfig, err := newTrackerConfigService().ResolveArcReviewWatchConfig(cfg.repoRoot)
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
	err = runArcReviewWatchPollLoop(ctx, cfg.once, reviewWatchConfig.PollInterval, func(context.Context) error {
		return runArcReviewWatchPollIteration(cfg)
	}, waitTrackerWatchPollInterval)
	emitArcReviewWatchFinished(ctx, cfg, reviewWatchConfig, err)
	return err
}

func resolveArcReviewWatchEventsPath(cfg arcReviewWatchCommandConfig) string {
	if strings.TrimSpace(cfg.eventsPath) != "" {
		return cfg.eventsPath
	}
	if cfg.stream {
		return ""
	}
	return filepath.Join(cfg.repoRoot, "runner-logs", "arc-review-watch.events.jsonl")
}

func arcReviewWatchEventSink(cfg arcReviewWatchCommandConfig) (contracts.EventSink, func()) {
	sinks := []contracts.EventSink{}
	closers := []func(){}
	if cfg.stream {
		sinks = append(sinks, contracts.NewStreamEventSink(os.Stdout))
	}
	if strings.TrimSpace(cfg.eventsPath) != "" {
		fileSink := contracts.NewFileEventSink(cfg.eventsPath)
		if cfg.stream {
			mirror := newMirrorEventSink(fileSink, 64)
			closers = append(closers, mirror.Close)
			sinks = append(sinks, mirror)
		} else {
			sinks = append(sinks, fileSink)
		}
	}
	closeFn := func() {
		for _, closer := range closers {
			closer()
		}
	}
	if len(sinks) == 0 {
		return nil, closeFn
	}
	if len(sinks) == 1 {
		return sinks[0], closeFn
	}
	return contracts.NewFanoutEventSink(sinks...), closeFn
}

func runArcReviewWatchPollLoop(ctx context.Context, once bool, pollInterval time.Duration, iterate trackerWatchPollIteration, wait trackerWatchPollWait) error {
	return runTrackerWatchPollLoop(ctx, once, pollInterval, iterate, wait)
}

func runArcReviewWatchPollIteration(cfg arcReviewWatchCommandConfig) error {
	reviewWatchConfig, err := newTrackerConfigService().ResolveArcReviewWatchConfig(cfg.repoRoot)
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

func defaultDiscoverArcReviewPRs(arcReviewWatchCommandConfig, arcReviewWatchConfig) ([]arcReviewDiscoveredPR, error) {
	return nil, nil
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
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed", "crashed", "cancelled", "canceled":
		return true
	default:
		return false
	}
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
		failed.Status = "crashed"
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
			Status:       "running",
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
		})
		replacement.LogPath = spec.LogPath
		if _, err := store.CreateSession(replacement); err != nil {
			return restarted, err
		}
		started, err := starter.StartArcReviewProcess(spec)
		if err != nil {
			replacement.Status = "crashed"
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
	if strings.ToLower(strings.TrimSpace(session.Status)) != "running" {
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
