package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

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
	if !cfg.dryRun {
		return nil
	}
	return nil
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
