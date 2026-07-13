package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/agent"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/envpreset"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

const defaultRunnerDaemonPollInterval = time.Second
const defaultRunnerDaemonEnvironmentsPath = "~/.yolo-runner/environments.yaml"
const runnerRetryableFailureRecoveryWindow = 30 * time.Minute

var errRunnerDaemonLockHeld = errors.New("runner daemon lock held")

var runRunnerDaemon = defaultRunRunnerDaemon

type runnerDaemonCommandConfig struct {
	queuePath         string
	environmentsPath  string
	presets           []string
	sourceRef         string
	itemID            string
	runnerID          string
	lockPath          string
	once              bool
	capacity          int
	pollInterval      time.Duration
	heartbeatInterval time.Duration
	leaseTTL          time.Duration
}

type runnerDaemonBuildOptions struct {
	handlers           runnerKindRegistry
	environmentPresets map[string]envpreset.Preset
	materializer       runnerWorkspaceMaterializer
	eventSink          contracts.EventSink
}

type runnerKindHandler func(context.Context, workitem.Item, envpreset.Workspace) (workqueue.Result, error)

type runnerKindRegistry map[workitem.Kind]runnerKindHandler

type runnerWorkspaceMaterializer func(context.Context, envpreset.Preset, string, bool) (envpreset.Workspace, error)

// runnerKindIsolated reports whether a work kind writes code and therefore
// needs a fully isolated, VCS-bearing workspace. Kinds that never write code —
// read-only model kinds (preflight, split) and the resolve-pr-comment stub,
// whose effect is applied by the owning source's HandleResult — get a
// lightweight parallel-safe read view instead.
func runnerKindIsolated(kind workitem.Kind) bool {
	switch kind {
	case workitem.KindImplement, workitem.KindFinalize:
		return true
	default:
		return false
	}
}

func runnerKindNeedsPresetWorkspace(kind workitem.Kind) bool {
	switch kind {
	case workitem.KindPRReview, workitem.KindResolvePRComment:
		return false
	default:
		return true
	}
}

func runnerItemNeedsPresetWorkspace(item workitem.Item) bool {
	if !runnerKindNeedsPresetWorkspace(item.Kind) {
		return false
	}
	if item.Kind != workitem.KindImplement {
		return true
	}
	payload, err := workitem.DecodeImplementPayload(item.Payload)
	if err != nil {
		return true
	}
	return !strings.EqualFold(strings.TrimSpace(payload.PromptContext.Metadata["origin"]), "arcpr-author")
}

func runnerDaemonCommand(args []string) int {
	fs := flag.NewFlagSet("yolo-agent runner", flag.ContinueOnError)
	queuePath := fs.String("queue", "", "Path to the SQLite work queue database")
	environmentsPath := fs.String("environments", "", "Path to the environment presets file")
	presets := fs.String("presets", "", "Comma-separated environment preset names this runner serves")
	sourceRef := fs.String("source-ref", "", "Only claim items with this exact source reference")
	itemID := fs.String("item-id", "", "Only claim this exact work item")
	runnerID := fs.String("runner-id", "", "Stable runner ID for registration and singleton locking")
	once := fs.Bool("once", false, "Claim and run at most one item, then exit")
	capacity := fs.Int("capacity", 1, "Runner capacity to register in the queue")
	pollInterval := fs.Duration("poll-interval", defaultRunnerDaemonPollInterval, "Delay between empty claim attempts")
	heartbeatInterval := fs.Duration("heartbeat-interval", 5*time.Second, "Interval for item and runner heartbeats")
	leaseTTL := fs.Duration("lease-ttl", 10*time.Minute, "Claim lease duration")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected runner argument: %s\n", fs.Arg(0))
		return 1
	}

	handler := runRunnerDaemon
	if handler == nil {
		handler = defaultRunRunnerDaemon
	}
	if err := handler(context.Background(), runnerDaemonCommandConfig{
		queuePath:         *queuePath,
		environmentsPath:  *environmentsPath,
		presets:           parseRunnerPresets(*presets),
		sourceRef:         *sourceRef,
		itemID:            *itemID,
		runnerID:          *runnerID,
		once:              *once,
		capacity:          *capacity,
		pollInterval:      *pollInterval,
		heartbeatInterval: *heartbeatInterval,
		leaseTTL:          *leaseTTL,
	}); err != nil {
		fmt.Fprintln(os.Stderr, agent.FormatActionableError(err))
		return 1
	}
	return 0
}

func defaultRunRunnerDaemon(ctx context.Context, cfg runnerDaemonCommandConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, err := normalizeRunnerDaemonConfig(cfg)
	if err != nil {
		return err
	}
	cfg = normalized

	lock, err := acquireRunnerDaemonLock(cfg.lockPath, cfg.runnerID)
	if err != nil {
		return err
	}
	defer func() {
		_ = lock.Release()
	}()

	store, err := workqueue.Open(cfg.queuePath)
	if err != nil {
		return err
	}
	defer store.Close()
	if cfg.itemID != "" {
		if _, err := store.RecoverRetryableFailure(cfg.itemID); err != nil {
			return err
		}
	} else if cfg.sourceRef != "" {
		failedSince := time.Now().UTC().Add(-runnerRetryableFailureRecoveryWindow)
		if _, err := store.RecoverRecentRetryableFailuresForSourceRef(cfg.sourceRef, failedSince); err != nil {
			return err
		}
	}

	runners, err := openRunnerRegistry(cfg.queuePath)
	if err != nil {
		return err
	}
	defer runners.Close()

	if err := runners.Register(cfg.runnerID, cfg.presets, cfg.capacity); err != nil {
		return err
	}

	environmentPresets, err := loadRunnerEnvironmentPresets(cfg.environmentsPath, cfg.presets)
	if err != nil {
		return err
	}
	handlers := defaultRunnerKindRegistry()
	eventSink := defaultRunnerDaemonEventSink(cfg.runnerID)
	handlers[workitem.KindImplement] = newRunnerImplementKindHandler(newRunnerImplementExecutorResolverForPresets(environmentPresets, eventSink))

	daemon, err := newRunnerDaemon(cfg, store, runners, runnerDaemonBuildOptions{
		handlers:           handlers,
		environmentPresets: environmentPresets,
		materializer:       envpreset.MaterializeWorkspace,
		eventSink:          eventSink,
	})
	if err != nil {
		return err
	}
	return daemon.Run(ctx)
}

func newRunnerDaemon(cfg runnerDaemonCommandConfig, store *workqueue.Store, runners *runnerRegistry, options runnerDaemonBuildOptions) (runnerDaemon, error) {
	if options.handlers == nil {
		options.handlers = defaultRunnerKindRegistry()
	}
	if len(options.environmentPresets) == 0 {
		return runnerDaemon{}, fmt.Errorf("environment presets are required for runner dispatch")
	}
	if options.materializer == nil {
		options.materializer = envpreset.MaterializeWorkspace
	}
	if options.eventSink == nil {
		options.eventSink = defaultRunnerDaemonEventSink(cfg.runnerID)
	}

	return runnerDaemon{
		store:              store,
		runners:            runners,
		handlers:           options.handlers,
		events:             options.eventSink,
		environmentPresets: options.environmentPresets,
		materialize:        options.materializer,
		cfg:                cfg,
	}, nil
}

type runnerDaemon struct {
	store    *workqueue.Store
	runners  *runnerRegistry
	handlers runnerKindRegistry
	events   contracts.EventSink
	cfg      runnerDaemonCommandConfig

	environmentPresets map[string]envpreset.Preset
	materialize        runnerWorkspaceMaterializer
}

type runnerDaemonItemResult struct {
	item workitem.Item
	err  error
}

func (d runnerDaemon) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if d.events == nil {
		d.events = defaultRunnerDaemonEventSink(d.cfg.runnerID)
	}

	capacity := d.cfg.capacity
	if capacity <= 0 {
		capacity = 1
	}
	d.emitRunnerRegisteredEvent(ctx, capacity)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	pollInterval := d.cfg.pollInterval
	if pollInterval <= 0 {
		pollInterval = defaultRunnerDaemonPollInterval
	}

	results := make(chan runnerDaemonItemResult, capacity)
	inFlightByPreset := map[string]int{}
	activeItems := map[string]workitem.Item{}
	active := 0
	claimedOnce := false

	for {
		if err := runCtx.Err(); err != nil {
			return err
		}
		for {
			select {
			case result := <-results:
				active--
				decrementRunnerPresetInFlight(inFlightByPreset, result.item.Preset)
				delete(activeItems, result.item.ID)
				if result.err != nil {
					return result.err
				}
				if d.cfg.once {
					return nil
				}
			default:
				goto drainedResults
			}
		}

	drainedResults:
		if err := d.runners.Heartbeat(d.cfg.runnerID); err != nil {
			return err
		}
		d.emitRunnerAliveEvent(runCtx, currentRunnerDaemonItem(activeItems))
		if _, err := d.store.RequeueStale(time.Now().UTC()); err != nil {
			return err
		}

		for active < capacity && !(d.cfg.once && claimedOnce) {
			claimPresets := d.claimableRunnerPresets(inFlightByPreset)
			if len(claimPresets) == 0 {
				break
			}

			var item *workitem.Item
			var err error
			if d.cfg.itemID != "" {
				item, err = d.store.ClaimForItemID(d.cfg.runnerID, claimPresets, d.cfg.itemID, d.cfg.leaseTTL)
			} else {
				item, err = d.store.ClaimForSourceRef(d.cfg.runnerID, claimPresets, d.cfg.sourceRef, d.cfg.leaseTTL)
			}
			if err != nil {
				return err
			}
			if item == nil {
				break
			}

			active++
			claimedOnce = true
			incrementRunnerPresetInFlight(inFlightByPreset, item.Preset)
			activeItems[item.ID] = *item
			go func(item workitem.Item) {
				results <- runnerDaemonItemResult{
					item: item,
					err:  d.runClaimedItem(runCtx, item),
				}
			}(*item)
		}

		if d.cfg.once {
			if !claimedOnce {
				return nil
			}
			result, err := d.waitRunnerDaemonOnceItemResult(runCtx, results, pollInterval, activeItems)
			if err != nil {
				return err
			}
			active--
			decrementRunnerPresetInFlight(inFlightByPreset, result.item.Preset)
			delete(activeItems, result.item.ID)
			return result.err
		}

		if active == 0 {
			if err := waitRunnerDaemonPollInterval(runCtx, pollInterval); err != nil {
				return err
			}
			continue
		}

		result, err := waitRunnerDaemonItemResultOrPoll(runCtx, results, pollInterval)
		if err != nil {
			return err
		}
		if result == nil {
			continue
		}
		active--
		decrementRunnerPresetInFlight(inFlightByPreset, result.item.Preset)
		delete(activeItems, result.item.ID)
		if result.err != nil {
			return result.err
		}
	}
}

func (d runnerDaemon) waitRunnerDaemonOnceItemResult(ctx context.Context, results <-chan runnerDaemonItemResult, interval time.Duration, activeItems map[string]workitem.Item) (runnerDaemonItemResult, error) {
	for {
		result, err := waitRunnerDaemonItemResultOrPoll(ctx, results, interval)
		if err != nil {
			return runnerDaemonItemResult{}, err
		}
		if result != nil {
			return *result, nil
		}
		if err := d.runners.Heartbeat(d.cfg.runnerID); err != nil {
			return runnerDaemonItemResult{}, err
		}
		d.emitRunnerAliveEvent(ctx, currentRunnerDaemonItem(activeItems))
	}
}

func currentRunnerDaemonItem(activeItems map[string]workitem.Item) *workitem.Item {
	for _, item := range activeItems {
		itemCopy := item
		return &itemCopy
	}
	return nil
}

func (d runnerDaemon) emitRunnerRegisteredEvent(ctx context.Context, capacity int) {
	if d.events == nil {
		return
	}
	event := contracts.NewEvent(contracts.EventTypeAgentStarted, contracts.EventIdentity{RunnerID: d.cfg.runnerID})
	event.Proc = d.cfg.runnerID
	event.Metadata = map[string]string{
		"pid":      fmt.Sprintf("%d", os.Getpid()),
		"presets":  strings.Join(d.cfg.presets, ","),
		"capacity": fmt.Sprintf("%d", capacity),
	}
	_ = d.events.Emit(ctx, event)
}

func (d runnerDaemon) emitRunnerAliveEvent(ctx context.Context, current *workitem.Item) {
	if d.events == nil {
		return
	}
	event := contracts.NewEvent(contracts.EventTypeAgentHeartbeat, contracts.EventIdentity{RunnerID: d.cfg.runnerID})
	event.Proc = d.cfg.runnerID
	event.Metadata = map[string]string{
		"heartbeat_age": "0s",
	}
	if current != nil {
		event.ItemID = current.ID
		event.Source = current.Source
		event.SourceRef = current.SourceRef
		event.Kind = string(current.Kind)
		event.Preset = current.Preset
		event.Attempt = current.Attempt
		event.MaxAttempts = current.MaxAttempts
		if current.Attempt > 0 {
			event.RetryCount = current.Attempt - 1
		}
		event.Metadata["current_item_id"] = current.ID
		event.Metadata["current_item_source_ref"] = current.SourceRef
	}
	_ = d.events.Emit(ctx, event)
}

func (d runnerDaemon) claimableRunnerPresets(inFlightByPreset map[string]int) []string {
	presets := normalizeRunnerPresets(d.cfg.presets)
	claimable := make([]string, 0, len(presets))
	for _, presetName := range presets {
		limit := 0
		if preset, ok := d.environmentPresets[presetName]; ok {
			limit = preset.Limits.MaxConcurrent
		}
		if limit > 0 && inFlightByPreset[presetName] >= limit {
			continue
		}
		claimable = append(claimable, presetName)
	}
	return claimable
}

func incrementRunnerPresetInFlight(inFlightByPreset map[string]int, preset string) {
	preset = strings.TrimSpace(preset)
	if preset == "" {
		return
	}
	inFlightByPreset[preset]++
}

func decrementRunnerPresetInFlight(inFlightByPreset map[string]int, preset string) {
	preset = strings.TrimSpace(preset)
	if preset == "" {
		return
	}
	inFlightByPreset[preset]--
	if inFlightByPreset[preset] <= 0 {
		delete(inFlightByPreset, preset)
	}
}

func waitRunnerDaemonItemResult(ctx context.Context, results <-chan runnerDaemonItemResult) (runnerDaemonItemResult, error) {
	select {
	case result := <-results:
		return result, nil
	case <-ctx.Done():
		return runnerDaemonItemResult{}, ctx.Err()
	}
}

func waitRunnerDaemonItemResultOrPoll(ctx context.Context, results <-chan runnerDaemonItemResult, interval time.Duration) (*runnerDaemonItemResult, error) {
	timer := time.NewTimer(interval)
	defer timer.Stop()

	select {
	case result := <-results:
		return &result, nil
	case <-timer.C:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (d runnerDaemon) runClaimedItem(ctx context.Context, item workitem.Item) error {
	startedAt := time.Now().UTC()
	d.emitClaimedItemEvent(ctx, contracts.EventTypeAgentStarted, item, "started", nil, startedAt)

	handler, ok := d.handlers[item.Kind]
	if !ok || handler == nil {
		cause := fmt.Errorf("no runner handler registered for kind %q", item.Kind)
		d.emitClaimedItemEvent(ctx, contracts.EventTypeAgentFinished, item, string(workqueue.ResultStatusFailed), map[string]string{"reason": cause.Error()}, time.Now().UTC())
		return d.failClaimedItem(item, startedAt, cause)
	}

	itemCtx, cancel := context.WithCancel(ctx)
	heartbeatDone, heartbeatErrs := startRunnerItemHeartbeat(itemCtx, d.store, item.ID, d.cfg.runnerID, d.cfg.heartbeatInterval)

	workspace, handlerErr := d.materializeClaimedWorkspace(itemCtx, item)
	var result workqueue.Result
	if handlerErr == nil {
		result, handlerErr = handler(itemCtx, item, workspace)
	}
	cancel()
	<-heartbeatDone

	var heartbeatErr error
	if err, ok := <-heartbeatErrs; ok {
		heartbeatErr = err
	}
	if cleanupErr := cleanupMaterializedWorkspace(workspace); cleanupErr != nil && handlerErr == nil {
		handlerErr = cleanupErr
	}
	if heartbeatErr != nil && handlerErr == nil {
		handlerErr = heartbeatErr
	}
	if handlerErr != nil {
		d.emitClaimedItemEvent(ctx, contracts.EventTypeAgentFinished, item, string(workqueue.ResultStatusFailed), map[string]string{"reason": handlerErr.Error()}, time.Now().UTC())
		return d.failClaimedItem(item, startedAt, handlerErr)
	}

	if result.StartedAt.IsZero() {
		result.StartedAt = startedAt
	}
	if result.FinishedAt.IsZero() {
		result.FinishedAt = time.Now().UTC()
	}

	var finishErr error
	status := workqueue.ResultStatusCompleted
	switch result.Status {
	case workqueue.ResultStatusBlocked:
		status = workqueue.ResultStatusBlocked
		finishErr = d.store.Block(item.ID, result)
	case workqueue.ResultStatusFailed:
		status = workqueue.ResultStatusFailed
		finishErr = d.store.Fail(item.ID, result)
	default:
		finishErr = d.store.Complete(item.ID, result)
	}
	if finishErr != nil {
		return finishErr
	}
	d.emitClaimedItemEvent(ctx, contracts.EventTypeAgentFinished, item, string(status), nil, result.FinishedAt)
	return nil
}

func (d runnerDaemon) emitClaimedItemEvent(ctx context.Context, eventType contracts.EventType, item workitem.Item, message string, metadata map[string]string, timestamp time.Time) {
	if d.events == nil {
		return
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	event := contracts.NewEvent(eventType, contracts.EventIdentity{
		Source:    item.Source,
		SourceRef: item.SourceRef,
		Kind:      string(item.Kind),
		Preset:    item.Preset,
		RunnerID:  d.cfg.runnerID,
	})
	event.Proc = d.cfg.runnerID
	event.ItemID = item.ID
	event.Message = message
	event.Metadata = metadata
	event.Timestamp = timestamp
	event.Attempt = item.Attempt
	if item.Attempt > 0 {
		event.RetryCount = item.Attempt - 1
	}
	event.MaxAttempts = item.MaxAttempts
	_ = d.events.Emit(ctx, event)
}

func (d runnerDaemon) materializeClaimedWorkspace(ctx context.Context, item workitem.Item) (envpreset.Workspace, error) {
	if len(d.environmentPresets) == 0 {
		return envpreset.Workspace{}, fmt.Errorf("environment presets are required for runner dispatch")
	}

	presetName := strings.TrimSpace(item.Preset)
	preset, ok := d.environmentPresets[presetName]
	if !ok {
		return envpreset.Workspace{}, fmt.Errorf("environment preset %q is not defined", presetName)
	}
	if !runnerItemNeedsPresetWorkspace(item) {
		return envpreset.Workspace{Cleanup: func() error { return nil }}, nil
	}

	materialize := d.materialize
	if materialize == nil {
		materialize = envpreset.MaterializeWorkspace
	}
	workspace, err := materialize(ctx, preset, item.ID, runnerKindIsolated(item.Kind))
	if err != nil {
		return envpreset.Workspace{}, fmt.Errorf("materialize workspace for item %q preset %q: %w", item.ID, presetName, err)
	}
	if workspace.Cleanup == nil {
		workspace.Cleanup = func() error { return nil }
	}
	return workspace, nil
}

func cleanupMaterializedWorkspace(workspace envpreset.Workspace) error {
	if workspace.Cleanup == nil {
		return nil
	}
	return workspace.Cleanup()
}

func (d runnerDaemon) failClaimedItem(item workitem.Item, startedAt time.Time, cause error) error {
	payload, err := json.Marshal(map[string]any{
		"status":  string(workqueue.ResultStatusFailed),
		"kind":    string(item.Kind),
		"item_id": item.ID,
		"reason":  cause.Error(),
	})
	if err != nil {
		return err
	}
	return d.store.Fail(item.ID, workqueue.Result{
		Payload:    payload,
		StartedAt:  startedAt,
		FinishedAt: time.Now().UTC(),
	})
}

type runnerItemHeartbeatStore interface {
	Heartbeat(itemID string, runnerID string) error
}

const runnerItemHeartbeatBusyRetryDelay = 250 * time.Millisecond

func startRunnerItemHeartbeat(ctx context.Context, store runnerItemHeartbeatStore, itemID string, runnerID string, interval time.Duration) (<-chan struct{}, <-chan error) {
	done := make(chan struct{})
	errs := make(chan error, 1)

	go func() {
		defer close(done)
		defer close(errs)
		if err := heartbeatRunnerItem(ctx, store, itemID, runnerID); err != nil {
			errs <- err
			return
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := heartbeatRunnerItem(ctx, store, itemID, runnerID); err != nil {
					errs <- err
					return
				}
			}
		}
	}()

	return done, errs
}

func heartbeatRunnerItem(ctx context.Context, store runnerItemHeartbeatStore, itemID string, runnerID string) error {
	for {
		err := store.Heartbeat(itemID, runnerID)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return nil
		}
		if !isTransientSQLiteBusyError(err) {
			return err
		}

		timer := time.NewTimer(runnerItemHeartbeatBusyRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func isTransientSQLiteBusyError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") ||
		strings.Contains(message, "database is locked") ||
		strings.Contains(message, "database table is locked") ||
		strings.Contains(message, "database is busy")
}

func defaultRunnerKindRegistry() runnerKindRegistry {
	registry := runnerKindRegistry{}
	for _, kind := range []workitem.Kind{
		workitem.KindReview,
		workitem.KindSplit,
	} {
		registry[kind] = stubRunnerKindHandler
	}
	registry[workitem.KindImplement] = newRunnerImplementKindHandler(defaultRunnerImplementExecutorResolver)
	registry[workitem.KindPreflight] = newRunnerPreflightKindHandler(defaultRunnerPreflightAgentResolver)
	registry[workitem.KindPRReview] = newRunnerPRReviewKindHandler(defaultRunnerPRReviewRuntimeResolver)
	registry[workitem.KindFinalize] = newRunnerFinalizeKindHandler()
	registry[workitem.KindResolvePRComment] = echoRunnerKindHandler
	return registry
}

func stubRunnerKindHandler(_ context.Context, item workitem.Item, _ envpreset.Workspace) (workqueue.Result, error) {
	payload, err := json.Marshal(map[string]any{
		"status":  "stubbed",
		"kind":    string(item.Kind),
		"item_id": item.ID,
	})
	if err != nil {
		return workqueue.Result{}, err
	}
	return workqueue.Result{Payload: payload}, nil
}

// echoRunnerKindHandler is a stub handler that echoes the item payload back as
// its result. It performs no work itself: kinds whose effect is applied by the
// owning source's HandleResult (resolve-pr-comment resolves a comment through
// internal/sources/arcpr/writeback.go) only need a completed result so the
// queue marks the item done. The runner never writes to Arcanum directly.
func echoRunnerKindHandler(_ context.Context, item workitem.Item, _ envpreset.Workspace) (workqueue.Result, error) {
	return workqueue.Result{Payload: item.Payload}, nil
}

func loadRunnerEnvironmentPresets(path string, requiredPresets []string) (map[string]envpreset.Preset, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultRunnerDaemonEnvironmentsPath
	}

	presets, err := envpreset.Load(path)
	if err != nil {
		return nil, err
	}

	for _, preset := range normalizeRunnerPresets(requiredPresets) {
		if _, ok := presets[preset]; !ok {
			return nil, fmt.Errorf("environment preset %q is not defined in %s", preset, path)
		}
	}
	return presets, nil
}

func normalizeRunnerDaemonConfig(cfg runnerDaemonCommandConfig) (runnerDaemonCommandConfig, error) {
	cfg.queuePath = strings.TrimSpace(cfg.queuePath)
	if cfg.queuePath == "" {
		return runnerDaemonCommandConfig{}, fmt.Errorf("--queue is required")
	}
	if !strings.HasPrefix(cfg.queuePath, "file:") && cfg.queuePath != ":memory:" {
		abs, err := filepath.Abs(cfg.queuePath)
		if err != nil {
			return runnerDaemonCommandConfig{}, fmt.Errorf("resolve queue path %q: %w", cfg.queuePath, err)
		}
		cfg.queuePath = abs
	}

	cfg.presets = normalizeRunnerPresets(cfg.presets)
	if len(cfg.presets) == 0 {
		return runnerDaemonCommandConfig{}, fmt.Errorf("--presets is required")
	}
	cfg.sourceRef = strings.TrimSpace(cfg.sourceRef)
	cfg.itemID = strings.TrimSpace(cfg.itemID)
	cfg.runnerID = strings.TrimSpace(cfg.runnerID)
	if cfg.runnerID == "" {
		cfg.runnerID = defaultRunnerID(cfg.presets)
	}
	if cfg.capacity <= 0 {
		return runnerDaemonCommandConfig{}, fmt.Errorf("--capacity must be greater than 0")
	}
	if cfg.pollInterval <= 0 {
		return runnerDaemonCommandConfig{}, fmt.Errorf("--poll-interval must be greater than 0")
	}
	if cfg.heartbeatInterval <= 0 {
		return runnerDaemonCommandConfig{}, fmt.Errorf("--heartbeat-interval must be greater than 0")
	}
	if cfg.leaseTTL <= 0 {
		return runnerDaemonCommandConfig{}, fmt.Errorf("--lease-ttl must be greater than 0")
	}
	cfg.lockPath = strings.TrimSpace(cfg.lockPath)
	if cfg.lockPath == "" {
		cfg.lockPath = defaultRunnerDaemonLockPath(cfg.queuePath, cfg.runnerID)
	}
	return cfg, nil
}

func parseRunnerPresets(raw string) []string {
	return normalizeRunnerPresets(strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t' || r == ' '
	}))
}

func normalizeRunnerPresets(presets []string) []string {
	seen := map[string]bool{}
	normalized := make([]string, 0, len(presets))
	for _, preset := range presets {
		preset = strings.TrimSpace(preset)
		if preset == "" || seen[preset] {
			continue
		}
		seen[preset] = true
		normalized = append(normalized, preset)
	}
	return normalized
}

func defaultRunnerID(presets []string) string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "local"
	}
	return host + "-" + strings.Join(presets, "-")
}

func defaultRunnerDaemonLockPath(queuePath string, runnerID string) string {
	if strings.HasPrefix(queuePath, "file:") || queuePath == ":memory:" {
		return filepath.Join(os.TempDir(), "yolo-runner-"+safeRunnerIDForPath(runnerID)+".lock")
	}
	return filepath.Join(filepath.Dir(queuePath), "runner-"+safeRunnerIDForPath(runnerID)+".lock")
}

func defaultRunnerDaemonEventSink(runnerID string) contracts.EventSink {
	eventsDir := defaultYoloRunnerEventsDirOrEmpty()
	if eventsDir == "" {
		return nil
	}
	return contracts.NewFileEventSink(filepath.Join(eventsDir, safeRunnerIDForPath(runnerID)+".jsonl"))
}

func safeRunnerIDForPath(runnerID string) string {
	var b strings.Builder
	for _, r := range runnerID {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "runner"
	}
	return b.String()
}

func waitRunnerDaemonPollInterval(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type runnerDaemonLock struct {
	path string
	file *os.File
}

func acquireRunnerDaemonLock(lockPath string, runnerID string) (*runnerDaemonLock, error) {
	lockPath = strings.TrimSpace(lockPath)
	if lockPath == "" {
		return nil, errors.New("runner daemon lock path is required")
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("cannot create runner daemon lock directory for %s: %w", lockPath, err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("cannot open runner daemon lock at %s: %w", lockPath, err)
	}
	if err := lockTrackerWatchFile(file); err != nil {
		_ = file.Close()
		if errors.Is(err, errTrackerWatchLockHeld) {
			return nil, fmt.Errorf("%w at %s", errRunnerDaemonLockHeld, lockPath)
		}
		return nil, fmt.Errorf("cannot acquire runner daemon lock at %s: %w", lockPath, err)
	}
	if err := file.Truncate(0); err != nil {
		_ = unlockTrackerWatchFile(file)
		_ = file.Close()
		return nil, fmt.Errorf("cannot update runner daemon lock at %s: %w", lockPath, err)
	}
	if _, err := fmt.Fprintf(file, "runner_id=%s\npid=%d\n", runnerID, os.Getpid()); err != nil {
		_ = unlockTrackerWatchFile(file)
		_ = file.Close()
		return nil, fmt.Errorf("cannot update runner daemon lock at %s: %w", lockPath, err)
	}
	return &runnerDaemonLock{path: lockPath, file: file}, nil
}

func (l *runnerDaemonLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	unlockErr := unlockTrackerWatchFile(file)
	closeErr := file.Close()
	if unlockErr != nil {
		return fmt.Errorf("cannot release runner daemon lock at %s: %w", l.path, unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("cannot close runner daemon lock at %s: %w", l.path, closeErr)
	}
	return nil
}

type runnerRegistry struct {
	db *sql.DB
}

func openRunnerRegistry(queuePath string) (*runnerRegistry, error) {
	db, err := sql.Open("sqlite", queuePath)
	if err != nil {
		return nil, fmt.Errorf("open runner registry: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure runner registry busy_timeout: %w", err)
	}
	return &runnerRegistry{db: db}, nil
}

func (r *runnerRegistry) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}

func (r *runnerRegistry) Register(runnerID string, presets []string, capacity int) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("runner registry is not open")
	}
	now := formatRunnerDaemonTime(time.Now().UTC())
	_, err := r.db.Exec(`
INSERT INTO runners (id, pid, presets, capacity, started_at, heartbeat_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	pid = excluded.pid,
	presets = excluded.presets,
	capacity = excluded.capacity,
	started_at = excluded.started_at,
	heartbeat_at = excluded.heartbeat_at`,
		runnerID,
		os.Getpid(),
		strings.Join(presets, ","),
		capacity,
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("register runner %q: %w", runnerID, err)
	}
	return nil
}

func (r *runnerRegistry) Heartbeat(runnerID string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("runner registry is not open")
	}
	result, err := r.db.Exec(`
UPDATE runners
SET pid = ?, heartbeat_at = ?
WHERE id = ?`,
		os.Getpid(),
		formatRunnerDaemonTime(time.Now().UTC()),
		runnerID,
	)
	if err != nil {
		return fmt.Errorf("heartbeat runner %q: %w", runnerID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("heartbeat runner %q rows affected: %w", runnerID, err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func formatRunnerDaemonTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
