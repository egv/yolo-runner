package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/agent"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/startrek"
)

var errTrackerWatchLockHeld = errors.New("tracker-watch lock held")

const (
	defaultTrackerWatchResilientMaxConsecutiveFailures = 20
	trackerWatchStartrekNeedsInfoLabel                 = "needs-info"
	trackerWatchStartrekNeedsInfoMarker                = "needs-info"
	trackerWatchStartrekSplitVersion                   = "strict-v1"
	trackerWatchStartrekProcessingLabel                = "yolo-agent-in-progress"
)

type trackerWatchLock struct {
	path string
	file *os.File
}

type trackerWatchPollIteration func(context.Context) error
type trackerWatchIterationErrorHandler func(error)
type trackerWatchPollIntervalProvider func() time.Duration
type trackerWatchPollWait func(context.Context, time.Duration) error

func defaultRunTrackerWatch(ctx context.Context, cfg trackerWatchConfig) error {
	if cfg.dryRun {
		return errors.New("tracker-watch --dry-run is not supported by the source startrek compatibility shim")
	}
	cfg.repoRoot = strings.TrimSpace(cfg.repoRoot)
	if cfg.repoRoot == "" {
		cfg.repoRoot = "."
	}
	profile := strings.TrimSpace(cfg.profile)
	if profile == "" {
		resolved, err := resolveTrackerProfile(cfg.repoRoot, profile, "", os.Getenv)
		if err != nil {
			return err
		}
		profile = resolved.Name
	}
	handler := runSourceStartrek
	if handler == nil {
		handler = defaultRunSourceStartrek
	}
	return handler(ctx, sourceStartrekCommandConfig{
		repoRoot:   cfg.repoRoot,
		profile:    profile,
		once:       cfg.once,
		stream:     cfg.stream,
		eventsPath: cfg.eventsPath,
		eventSink:  cfg.eventSink,
	})
}

// runResilientWatchPollLoop runs a watch poll loop that emits a warning event
// per failed iteration and keeps polling. A context cancellation after the
// loop has recovered from an earlier failure is a clean shutdown, not an error.
func runResilientWatchPollLoop(ctx context.Context, once bool, pollInterval trackerWatchPollIntervalProvider, iterate trackerWatchPollIteration, eventSink contracts.EventSink, wait trackerWatchPollWait) error {
	sawIterationError := false
	recoveredFromIterationError := false
	err := runTrackerWatchPollLoop(ctx, once, pollInterval, func(ctx context.Context) error {
		err := iterate(ctx)
		if err == nil && sawIterationError {
			recoveredFromIterationError = true
		}
		return err
	}, func(err error) {
		sawIterationError = true
		emitTrackerWatchIterationWarning(ctx, eventSink, err)
	}, defaultTrackerWatchResilientMaxConsecutiveFailures, wait)
	if errors.Is(err, context.Canceled) && recoveredFromIterationError {
		return nil
	}
	return err
}

// dynamicWatchPollIntervalProvider re-resolves the poll interval before each
// wait and falls back to the last successfully resolved value on error.
func dynamicWatchPollIntervalProvider(lastGood time.Duration, resolve func() (time.Duration, error)) trackerWatchPollIntervalProvider {
	return func() time.Duration {
		if resolve == nil {
			return lastGood
		}
		interval, err := resolve()
		if err != nil {
			return lastGood
		}
		lastGood = interval
		return lastGood
	}
}

func resolveWatchEventsPath(eventsPath string, stream bool, repoRoot string, defaultName string) string {
	if strings.TrimSpace(eventsPath) != "" {
		return eventsPath
	}
	if stream {
		return ""
	}
	return filepath.Join(repoRoot, "runner-logs", defaultName)
}

func resolveSourceEventsPath(eventsPath string, procID string) string {
	if strings.TrimSpace(eventsPath) != "" {
		return eventsPath
	}
	eventsDir := defaultYoloRunnerEventsDirOrEmpty()
	if eventsDir == "" {
		return ""
	}
	return filepath.Join(eventsDir, safeRunnerIDForPath(procID)+".jsonl")
}

func watchEventSink(stream bool, eventsPath string) (contracts.EventSink, func()) {
	return watchEventSinkWithWriter(stream, eventsPath, os.Stdout)
}

func watchEventSinkWithWriter(stream bool, eventsPath string, streamWriter io.Writer) (contracts.EventSink, func()) {
	sinks := []contracts.EventSink{}
	closers := []func(){}
	if stream {
		sinks = append(sinks, contracts.NewStreamEventSink(streamWriter))
	}
	if strings.TrimSpace(eventsPath) != "" {
		fileSink := contracts.NewFileEventSink(eventsPath)
		if stream {
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

func runTrackerWatchPollLoop(ctx context.Context, once bool, pollInterval trackerWatchPollIntervalProvider, iterate trackerWatchPollIteration, onIterationError trackerWatchIterationErrorHandler, maxConsecutiveFailures int, wait trackerWatchPollWait) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if pollInterval == nil {
		return errors.New("tracker-watch poll interval provider is required")
	}
	if iterate == nil {
		return errors.New("tracker-watch poll iteration is required")
	}
	if wait == nil {
		return errors.New("tracker-watch poll wait is required")
	}

	consecutiveFailures := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := iterate(ctx); err != nil {
			if onIterationError != nil {
				onIterationError(err)
			}
			if once {
				return err
			}
			consecutiveFailures++
			if maxConsecutiveFailures > 0 && consecutiveFailures >= maxConsecutiveFailures {
				return err
			}
		} else {
			consecutiveFailures = 0
			if once {
				return nil
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := wait(ctx, pollInterval()); err != nil {
			return err
		}
	}
}

type trackerWatchStartrekBackend interface {
	contracts.StorageBackend
	RemoveLabel(ctx context.Context, issueID string, label string) error
	AddLabel(ctx context.Context, issueID string, label string) error
	CreateIssue(ctx context.Context, opts startrek.IssueCreateOptions) (startrek.Issue, error)
	CreateIssueLink(ctx context.Context, opts startrek.IssueLinkCreateOptions) error
	GetIssueComments(ctx context.Context, issueID string) ([]startrek.IssueComment, error)
	CreateIssueComment(ctx context.Context, issueID string, opts startrek.IssueCommentCreateOptions) (startrek.IssueComment, error)
}

func emitTrackerWatchEvent(ctx context.Context, sink contracts.EventSink, event contracts.Event) {
	if sink == nil {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	_ = sink.Emit(ctx, event)
}

func emitTrackerWatchIterationWarning(ctx context.Context, sink contracts.EventSink, err error) {
	if err == nil {
		return
	}
	emitTrackerWatchEvent(ctx, sink, contracts.Event{
		Type:    contracts.EventTypeAgentProgress,
		Message: agent.FormatActionableError(err),
		Metadata: compactTrackerWatchMetadata(map[string]string{
			"phase": "watch_iteration",
			"level": "warning",
		}),
	})
}

func compactTrackerWatchMetadata(metadata map[string]string) map[string]string {
	out := copyStringMap(metadata)
	for key, value := range out {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			delete(out, key)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildTrackerWatchStartrekBackend(profile resolvedTrackerProfile, trackerAgentConfig trackerAgentConfig) (*startrek.StorageBackend, error) {
	if profile.Tracker.Startrek == nil {
		return nil, errors.New("tracker.startrek settings are required")
	}
	tokenEnv := strings.TrimSpace(profile.Tracker.Startrek.TokenEnv)
	token := ""
	if tokenEnv != "" {
		token = os.Getenv(tokenEnv)
	}
	backend, err := startrek.NewStorageBackend(startrek.Config{
		Endpoint:   profile.Tracker.Startrek.Endpoint,
		Token:      token,
		ReadyLabel: trackerAgentConfig.Labels.Ready,
		StatusTransitions: startrek.StatusTransitionNames{
			Ready:               trackerAgentConfig.StatusTransitions.Ready,
			InProgress:          trackerAgentConfig.StatusTransitions.InProgress,
			Completed:           trackerAgentConfig.StatusTransitions.Completed,
			Blocked:             trackerAgentConfig.StatusTransitions.Blocked,
			Failed:              trackerAgentConfig.StatusTransitions.Failed,
			CompletedResolution: trackerAgentConfig.StatusTransitions.CompletedResolution,
		},
	})
	if err != nil {
		return nil, err
	}
	return backend, nil
}

func buildTrackerWatchRunner(repoRoot string) (contracts.AgentRunner, trackerWatchRunnerDefaults, error) {
	defaults, err := loadYoloAgentConfigDefaults(repoRoot)
	if err != nil {
		return nil, trackerWatchRunnerDefaults{}, err
	}
	catalog, err := loadCodingAgentsCatalog(repoRoot)
	if err != nil {
		return nil, trackerWatchRunnerDefaults{}, err
	}
	resolved := trackerWatchRunnerDefaults{Config: defaults}
	runner, err := buildAgentRunner(catalog, defaults.Backend, defaults.Model, resolved.RunnerTimeoutValue())
	if err != nil {
		return nil, trackerWatchRunnerDefaults{}, err
	}
	return runner, resolved, nil
}

type trackerWatchRunnerDefaults struct {
	Config yoloAgentConfigDefaults
}

func (d trackerWatchRunnerDefaults) RunnerTimeoutValue() time.Duration {
	if d.Config.RunnerTimeout != nil {
		return *d.Config.RunnerTimeout
	}
	return 0
}

func (d trackerWatchRunnerDefaults) WatchdogTimeoutValue() time.Duration {
	if d.Config.WatchdogTimeout != nil {
		return *d.Config.WatchdogTimeout
	}
	return 10 * time.Minute
}

func (d trackerWatchRunnerDefaults) WatchdogIntervalValue() time.Duration {
	if d.Config.WatchdogInterval != nil {
		return *d.Config.WatchdogInterval
	}
	return 5 * time.Second
}

func (d trackerWatchRunnerDefaults) RetryBudgetValue() int {
	if d.Config.RetryBudget != nil {
		return *d.Config.RetryBudget
	}
	return 5
}

func (d trackerWatchRunnerDefaults) ConcurrencyValue() int {
	if d.Config.Concurrency != nil {
		return *d.Config.Concurrency
	}
	return 1
}

func waitTrackerWatchPollInterval(ctx context.Context, interval time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}

	timer := time.NewTimer(interval)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func acquireTrackerWatchLock(lockPath string) (*trackerWatchLock, error) {
	lockPath = strings.TrimSpace(lockPath)
	if lockPath == "" {
		return nil, errors.New("tracker-watch lock path is required")
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("cannot create tracker-watch lock directory for %s: %w", lockPath, err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("cannot open tracker-watch lock at %s: %w", lockPath, err)
	}
	if err := lockTrackerWatchFile(file); err != nil {
		_ = file.Close()
		if errors.Is(err, errTrackerWatchLockHeld) {
			return nil, fmt.Errorf("tracker-watch lock is already held at %s", lockPath)
		}
		return nil, fmt.Errorf("cannot acquire tracker-watch lock at %s: %w", lockPath, err)
	}
	if err := file.Truncate(0); err != nil {
		_ = unlockTrackerWatchFile(file)
		_ = file.Close()
		return nil, fmt.Errorf("cannot update tracker-watch lock at %s: %w", lockPath, err)
	}
	if _, err := fmt.Fprintf(file, "pid=%d\n", os.Getpid()); err != nil {
		_ = unlockTrackerWatchFile(file)
		_ = file.Close()
		return nil, fmt.Errorf("cannot update tracker-watch lock at %s: %w", lockPath, err)
	}
	return &trackerWatchLock{
		path: lockPath,
		file: file,
	}, nil
}

func (l *trackerWatchLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	unlockErr := unlockTrackerWatchFile(file)
	closeErr := file.Close()
	if unlockErr != nil {
		return fmt.Errorf("cannot release tracker-watch lock at %s: %w", l.path, unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("cannot close tracker-watch lock at %s: %w", l.path, closeErr)
	}
	return nil
}
