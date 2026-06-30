package sourcehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

const (
	defaultPollInterval           = time.Minute
	defaultMaxConsecutiveFailures = 20
)

var ErrLockHeld = errors.New("sourcehost lock held")

type Submission = workqueue.Submission
type Result = workqueue.Result

// Source is the source-adapter contract hosted by Run.
type Source interface {
	Name() string
	Poll(context.Context) ([]Submission, error)
	HandleResult(context.Context, workitem.Item, Result) ([]Submission, error)
}

// Options configures the sourcehost runtime loop.
type Options struct {
	Once                   bool
	PollInterval           time.Duration
	MaxConsecutiveFailures int
	ProcID                 string
	LockPath               string
	EventsPath             string
	EventsDir              string
	EventSink              contracts.EventSink
}

// Run hosts a source adapter against a workqueue store.
func Run(ctx context.Context, source Source, queue *workqueue.Store, opts Options) error {
	if ctx == nil {
		ctx = context.Background()
	}
	sourceName, err := validateRunInput(source, queue)
	if err != nil {
		return err
	}
	procID := resolveProcID(sourceName, opts.ProcID)
	eventSink := resolveEventSink(opts, procID)

	lock, err := acquireOptionalLock(opts.LockPath)
	if err != nil {
		return err
	}
	if lock != nil {
		defer func() {
			_ = lock.Release()
		}()
	}

	emit(ctx, eventSink, contracts.Event{
		Type:    contracts.EventTypeRunStarted,
		Message: "sourcehost started",
		Metadata: map[string]string{
			"component": "sourcehost",
			"source":    sourceName,
			"proc":      procID,
		},
	})
	err = runLoop(ctx, source, sourceName, queue, opts, procID, eventSink)
	metadata := map[string]string{
		"component": "sourcehost",
		"source":    sourceName,
		"proc":      procID,
	}
	if err != nil {
		metadata["error"] = err.Error()
	}
	emit(ctx, eventSink, contracts.Event{
		Type:     contracts.EventTypeRunFinished,
		Message:  "sourcehost finished",
		Metadata: metadata,
	})
	return err
}

func validateRunInput(source Source, queue *workqueue.Store) (string, error) {
	if source == nil {
		return "", errors.New("source is required")
	}
	sourceName := strings.TrimSpace(source.Name())
	if sourceName == "" {
		return "", errors.New("source name is required")
	}
	if queue == nil {
		return "", errors.New("workqueue store is required")
	}
	return sourceName, nil
}

func runLoop(ctx context.Context, source Source, sourceName string, queue *workqueue.Store, opts Options, procID string, eventSink contracts.EventSink) error {
	pollInterval := opts.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}
	maxConsecutiveFailures := opts.MaxConsecutiveFailures
	if maxConsecutiveFailures == 0 {
		maxConsecutiveFailures = defaultMaxConsecutiveFailures
	}

	consecutiveFailures := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		if err := runIteration(ctx, source, sourceName, queue, procID, eventSink); err != nil {
			consecutiveFailures++
			emitWarning(ctx, eventSink, sourceName, procID, err)
			if opts.Once || (maxConsecutiveFailures > 0 && consecutiveFailures >= maxConsecutiveFailures) {
				return err
			}
		} else {
			consecutiveFailures = 0
			emitHeartbeat(ctx, eventSink, sourceName, procID)
			if opts.Once {
				return nil
			}
		}

		if err := waitPollInterval(ctx, pollInterval); err != nil {
			return err
		}
	}
}

func runIteration(ctx context.Context, source Source, sourceName string, queue *workqueue.Store, procID string, eventSink contracts.EventSink) error {
	reaped, err := queue.RequeueStale(time.Now().UTC())
	if err != nil {
		return fmt.Errorf("requeue stale work items: %w", err)
	}
	if reaped > 0 {
		emit(ctx, eventSink, contracts.Event{
			Type:    contracts.EventTypeAgentProgress,
			Message: "sourcehost reaped stale work items",
			Metadata: map[string]string{
				"component": "sourcehost",
				"source":    sourceName,
				"proc":      procID,
				"reaped":    fmt.Sprintf("%d", reaped),
			},
		})
	}

	if err := consumeResults(ctx, source, sourceName, queue, procID); err != nil {
		return err
	}
	if err := pollAndEnqueue(ctx, source, sourceName, queue); err != nil {
		return err
	}
	return nil
}

func consumeResults(ctx context.Context, source Source, sourceName string, queue *workqueue.Store, procID string) error {
	results, err := queue.ListUnconsumedResults(sourceName)
	if err != nil {
		return fmt.Errorf("list unconsumed results for source %q: %w", sourceName, err)
	}
	for _, unconsumed := range results {
		if err := ctx.Err(); err != nil {
			return err
		}
		followUps, err := source.HandleResult(ctx, unconsumed.Item, unconsumed.Result)
		if err != nil {
			return fmt.Errorf("handle result for item %q: %w", unconsumed.Item.ID, err)
		}
		followUps, err = normalizeSubmissions(sourceName, followUps)
		if err != nil {
			return fmt.Errorf("handle result for item %q follow-ups: %w", unconsumed.Item.ID, err)
		}
		if err := queue.MarkConsumedWithFollowUps(unconsumed.Result.ItemID, procID, followUps); err != nil {
			return fmt.Errorf("mark result %q consumed: %w", unconsumed.Result.ItemID, err)
		}
	}
	return nil
}

func pollAndEnqueue(ctx context.Context, source Source, sourceName string, queue *workqueue.Store) error {
	submissions, err := source.Poll(ctx)
	if err != nil {
		return fmt.Errorf("poll source %q: %w", sourceName, err)
	}
	submissions, err = normalizeSubmissions(sourceName, submissions)
	if err != nil {
		return fmt.Errorf("poll source %q submissions: %w", sourceName, err)
	}
	for _, submission := range submissions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := queue.Enqueue(submission); err != nil {
			return fmt.Errorf("enqueue source %q submission %q: %w", sourceName, submission.IdempotencyKey, err)
		}
	}
	return nil
}

func normalizeSubmissions(sourceName string, submissions []workqueue.Submission) ([]workqueue.Submission, error) {
	normalized := make([]workqueue.Submission, 0, len(submissions))
	for _, submission := range submissions {
		submission.Source = strings.TrimSpace(submission.Source)
		if submission.Source == "" {
			submission.Source = sourceName
		}
		if submission.Source != sourceName {
			return nil, fmt.Errorf("submission source %q does not match sourcehost source %q", submission.Source, sourceName)
		}
		normalized = append(normalized, submission)
	}
	return normalized, nil
}

func waitPollInterval(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return nil
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

func resolveProcID(sourceName string, configured string) string {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		return configured
	}
	return fmt.Sprintf("%s-%d", sourceName, os.Getpid())
}

func resolveEventSink(opts Options, procID string) contracts.EventSink {
	sinks := []contracts.EventSink{}
	if opts.EventSink != nil {
		sinks = append(sinks, opts.EventSink)
	}
	if eventsPath := resolveEventsPath(opts, procID); eventsPath != "" {
		sinks = append(sinks, contracts.NewFileEventSink(eventsPath))
	}
	switch len(sinks) {
	case 0:
		return nil
	case 1:
		return sinks[0]
	default:
		return contracts.NewFanoutEventSink(sinks...)
	}
}

func resolveEventsPath(opts Options, procID string) string {
	if path := strings.TrimSpace(opts.EventsPath); path != "" {
		return path
	}
	dir := strings.TrimSpace(opts.EventsDir)
	if dir == "" {
		dir = defaultEventsDir()
	}
	if dir != "" {
		return filepath.Join(dir, sanitizeProcID(procID)+".jsonl")
	}
	return ""
}

func defaultEventsDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".yolo-runner", "events")
}

func sanitizeProcID(procID string) string {
	procID = strings.TrimSpace(procID)
	if procID == "" {
		return "sourcehost"
	}
	replacer := strings.NewReplacer(
		string(filepath.Separator), "_",
		" ", "_",
		":", "_",
	)
	return replacer.Replace(procID)
}

func emitWarning(ctx context.Context, sink contracts.EventSink, sourceName string, procID string, err error) {
	emit(ctx, sink, contracts.Event{
		Type:    contracts.EventTypeAgentProgress,
		Message: err.Error(),
		Metadata: map[string]string{
			"component": "sourcehost",
			"source":    sourceName,
			"proc":      procID,
			"level":     "warning",
		},
	})
}

func emitHeartbeat(ctx context.Context, sink contracts.EventSink, sourceName string, procID string) {
	emit(ctx, sink, contracts.Event{
		Type:    contracts.EventTypeAgentHeartbeat,
		Message: "sourcehost heartbeat",
		Metadata: map[string]string{
			"component": "sourcehost",
			"source":    sourceName,
			"proc":      procID,
		},
	})
}

func emit(ctx context.Context, sink contracts.EventSink, event contracts.Event) {
	if sink == nil {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	_ = sink.Emit(ctx, event)
}

type processLock struct {
	file *os.File
}

func acquireOptionalLock(path string) (*processLock, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create sourcehost lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open sourcehost lock %q: %w", path, err)
	}
	if err := lockSourcehostFile(file); err != nil {
		_ = file.Close()
		if errors.Is(err, ErrLockHeld) {
			return nil, ErrLockHeld
		}
		return nil, fmt.Errorf("lock sourcehost %q: %w", path, err)
	}
	if err := file.Truncate(0); err != nil {
		_ = unlockSourcehostFile(file)
		_ = file.Close()
		return nil, fmt.Errorf("truncate sourcehost lock %q: %w", path, err)
	}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		_ = unlockSourcehostFile(file)
		_ = file.Close()
		return nil, fmt.Errorf("write sourcehost lock %q: %w", path, err)
	}
	return &processLock{file: file}, nil
}

func (l *processLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := unlockSourcehostFile(l.file)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
