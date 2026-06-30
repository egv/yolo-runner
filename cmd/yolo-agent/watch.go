package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/egv/yolo-runner/v2/internal/agent"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/envpreset"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

const (
	defaultWatchSupervisorTickInterval = time.Second
	defaultWatchIdleCooldown           = 2 * time.Minute
	defaultWatchEnvironmentsPath       = defaultRunnerDaemonEnvironmentsPath
)

type watchCommandConfig struct {
	repoRoot         string
	environmentsPath string
	stream           bool
	tui              bool
	eventsPath       string
	tickInterval     time.Duration
	idleCooldown     time.Duration
	eventSink        contracts.EventSink
}

type watchConfigResolver interface {
	ResolveWatchConfig(repoRoot string) (watchConfig, error)
}

var runWatch = defaultRunWatch

var newWatchConfigService = func() watchConfigResolver {
	return newTrackerConfigService()
}

func watchCommand(args []string) int {
	fs := flag.NewFlagSet("yolo-agent watch", flag.ContinueOnError)
	repo := fs.String("repo", ".", "Repository root")
	environments := fs.String("environments", "", "Path to the environment presets file")
	stream := fs.Bool("stream", false, "Emit NDJSON events to stdout for piping into yolo-tui")
	tui := fs.Bool("tui", false, "Open yolo-tui and stream watch events into it")
	events := fs.String("events", "", "Path to JSONL events log")
	tickInterval := fs.Duration("tick-interval", defaultWatchSupervisorTickInterval, "Autoscaler queue-depth polling interval")
	idleCooldown := fs.Duration("idle-cooldown", defaultWatchIdleCooldown, "Idle duration before scaling runner pools down")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected watch argument: %s\n", fs.Arg(0))
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	handler := runWatch
	if handler == nil {
		handler = defaultRunWatch
	}
	if err := handler(ctx, watchCommandConfig{
		repoRoot:         *repo,
		environmentsPath: *environments,
		stream:           *stream,
		tui:              *tui,
		eventsPath:       *events,
		tickInterval:     *tickInterval,
		idleCooldown:     *idleCooldown,
	}); err != nil {
		if errors.Is(err, context.Canceled) {
			return 0
		}
		fmt.Fprintln(os.Stderr, agent.FormatActionableError(err))
		return 1
	}
	return 0
}

func defaultRunWatch(ctx context.Context, cfg watchCommandConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg.repoRoot = strings.TrimSpace(cfg.repoRoot)
	if cfg.repoRoot == "" {
		cfg.repoRoot = "."
	}
	if cfg.tickInterval <= 0 {
		return fmt.Errorf("--tick-interval must be greater than 0")
	}
	if cfg.idleCooldown < 0 {
		return fmt.Errorf("--idle-cooldown must be greater than or equal to 0")
	}

	configService := newWatchConfigService()
	if configService == nil {
		return errors.New("watch config service is required")
	}
	watchCfg, err := configService.ResolveWatchConfig(cfg.repoRoot)
	if err != nil {
		return err
	}

	store, err := workqueue.Open(watchCfg.QueuePath)
	if err != nil {
		return err
	}
	defer store.Close()

	mode := strings.TrimSpace(watchCfg.DefaultMode)
	if cfg.stream {
		mode = agentModeStream
	}
	if cfg.tui {
		mode = agentModeUI
	}
	stream := mode == agentModeStream || mode == agentModeUI
	var streamWriter io.Writer = os.Stdout
	if mode == agentModeUI {
		stdin, closeFn, err := launchYoloBoard(watchCfg.QueuePath)
		if err != nil {
			return fmt.Errorf("start yolo-board: %w", err)
		}
		defer func() {
			_ = closeFn()
		}()
		streamWriter = stdin
	}
	eventSink, closeEventSink := watchEventSinkWithWriter(stream, cfg.eventsPath, streamWriter)
	defer closeEventSink()
	if cfg.eventSink != nil {
		if eventSink != nil {
			eventSink = contracts.NewFanoutEventSink(cfg.eventSink, eventSink)
		} else {
			eventSink = cfg.eventSink
		}
	}

	environmentsPath := strings.TrimSpace(cfg.environmentsPath)
	if environmentsPath == "" {
		environmentsPath = defaultWatchEnvironmentsPath
	}
	supervisor := newWatchSupervisor(watchSupervisorConfig{
		Watch:         watchCfg,
		QueueDepth:    watchQueueDepthStore{store: store},
		SourceStarter: defaultWatchSourceStarter{repoRoot: cfg.repoRoot, eventSink: eventSink},
		RunnerStarter: defaultWatchRunnerStarter{queuePath: watchCfg.QueuePath, environmentsPath: environmentsPath, eventSink: eventSink},
		EventSink:     eventSink,
		TickInterval:  cfg.tickInterval,
		IdleCooldown:  cfg.idleCooldown,
	})
	return supervisor.Run(ctx)
}

type watchSupervisorConfig struct {
	Watch         watchConfig
	QueueDepth    watchQueueDepthProvider
	SourceStarter watchSourceStarter
	RunnerStarter watchRunnerStarter
	TickInterval  time.Duration
	IdleCooldown  time.Duration
	Now           func() time.Time
	EventSink     contracts.EventSink
}

type watchQueueDepthProvider interface {
	PendingDepth(ctx context.Context, source string, presets []string) (int, error)
}

type watchActiveDepthProvider interface {
	ActiveDepth(ctx context.Context, source string, presets []string) (int, error)
}

type watchQueueStateProvider interface {
	ListSources(ctx context.Context) ([]workqueue.SourceRow, error)
	ItemStateCounts(ctx context.Context) (map[string]int, error)
}

type watchSourceStarter interface {
	StartSource(ctx context.Context, source watchSourceConfig, queuePath string) (watchManagedProcess, error)
}

type watchRunnerStarter interface {
	StartRunner(ctx context.Context, pool watchRunnerPoolConfig, replica int) (watchManagedProcess, error)
}

type watchManagedProcess interface {
	Stop() error
	Wait() error
}

type watchSupervisor struct {
	cfg watchSupervisorConfig
}

func newWatchSupervisor(cfg watchSupervisorConfig) *watchSupervisor {
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = defaultWatchSupervisorTickInterval
	}
	if cfg.IdleCooldown < 0 {
		cfg.IdleCooldown = 0
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return &watchSupervisor{cfg: cfg}
}

func (s *watchSupervisor) Run(ctx context.Context) error {
	if s == nil {
		return errors.New("watch supervisor is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if s.cfg.QueueDepth == nil {
		return errors.New("watch queue depth provider is required")
	}
	if s.cfg.SourceStarter == nil {
		return errors.New("watch source starter is required")
	}
	if s.cfg.RunnerStarter == nil {
		return errors.New("watch runner starter is required")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sources, err := s.startSources(runCtx)
	if err != nil {
		return err
	}
	pools := s.newPoolStates()
	if err := s.reconcilePools(runCtx, pools); err != nil {
		_ = stopWatchProcesses(sources)
		return err
	}

	ticker := time.NewTicker(s.cfg.TickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-runCtx.Done():
			err := stopWatchPools(pools)
			err = errors.Join(err, stopWatchProcesses(sources))
			if err != nil {
				return err
			}
			return runCtx.Err()
		case <-ticker.C:
			if err := s.reconcilePools(runCtx, pools); err != nil {
				cancel()
				err = errors.Join(err, stopWatchPools(pools))
				err = errors.Join(err, stopWatchProcesses(sources))
				return err
			}
		}
	}
}

func (s *watchSupervisor) startSources(ctx context.Context) ([]watchManagedProcess, error) {
	handles := make([]watchManagedProcess, 0, len(s.cfg.Watch.Sources))
	for _, source := range s.cfg.Watch.Sources {
		handle, err := s.cfg.SourceStarter.StartSource(ctx, source, s.cfg.Watch.QueuePath)
		if err != nil {
			_ = stopWatchProcesses(handles)
			return nil, fmt.Errorf("start watch source %q: %w", source.Name, err)
		}
		handles = append(handles, handle)
	}
	return handles, nil
}

func (s *watchSupervisor) newPoolStates() map[string]*watchRunnerPoolState {
	sourceNames := map[string]string{}
	for _, source := range s.cfg.Watch.Sources {
		sourceNames[source.Name] = watchRuntimeSourceName(source)
	}

	states := make(map[string]*watchRunnerPoolState, len(s.cfg.Watch.RunnerPools))
	for _, pool := range s.cfg.Watch.RunnerPools {
		normalized := normalizeWatchRunnerPool(pool)
		states[normalized.Name] = &watchRunnerPoolState{
			pool:       normalized,
			sourceName: sourceNames[normalized.Source],
		}
	}
	return states
}

func (s *watchSupervisor) reconcilePools(ctx context.Context, states map[string]*watchRunnerPoolState) error {
	if err := s.emitQueueSnapshot(ctx); err != nil {
		return err
	}

	names := make([]string, 0, len(states))
	for name := range states {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		state := states[name]
		if err := s.reconcilePool(ctx, state); err != nil {
			return err
		}
	}
	return nil
}

func (s *watchSupervisor) emitQueueSnapshot(ctx context.Context) error {
	if s.cfg.EventSink == nil {
		return nil
	}
	metadata, err := s.queueSnapshotMetadata(ctx)
	if err != nil {
		return err
	}
	return s.cfg.EventSink.Emit(ctx, contracts.Event{
		Type:      contracts.EventTypeQueueSnapshot,
		Metadata:  metadata,
		Timestamp: s.cfg.Now(),
	})
}

func (s *watchSupervisor) queueSnapshotMetadata(ctx context.Context) (map[string]string, error) {
	metadata := map[string]string{}
	sourcePresets := s.queueSnapshotSourcePresets()
	sourceNames := make([]string, 0, len(sourcePresets))
	for sourceName := range sourcePresets {
		sourceNames = append(sourceNames, sourceName)
	}
	sort.Strings(sourceNames)

	for _, sourceName := range sourceNames {
		presets := sourcePresets[sourceName]
		pendingDepth := 0
		activeDepth := 0
		if len(presets) > 0 {
			var err error
			pendingDepth, err = s.cfg.QueueDepth.PendingDepth(ctx, sourceName, presets)
			if err != nil {
				return nil, fmt.Errorf("read queue snapshot pending depth for source %q: %w", sourceName, err)
			}
			activeDepth, err = s.activeDepth(ctx, sourceName, presets)
			if err != nil {
				return nil, fmt.Errorf("read queue snapshot active depth for source %q: %w", sourceName, err)
			}
		}
		metadata["depth."+sourceName+".pending"] = strconv.Itoa(pendingDepth)
		metadata["depth."+sourceName+".active"] = strconv.Itoa(activeDepth)
	}

	if provider, ok := s.cfg.QueueDepth.(watchQueueStateProvider); ok {
		sourceCounts, err := provider.ListSources(ctx)
		if err != nil {
			return nil, fmt.Errorf("read queue snapshot source state counts: %w", err)
		}
		sort.Slice(sourceCounts, func(i, j int) bool {
			if sourceCounts[i].Source == sourceCounts[j].Source {
				return sourceCounts[i].State < sourceCounts[j].State
			}
			return sourceCounts[i].Source < sourceCounts[j].Source
		})
		for _, count := range sourceCounts {
			metadata["state."+count.Source+"."+count.State] = strconv.Itoa(count.Count)
		}

		stateCounts, err := provider.ItemStateCounts(ctx)
		if err != nil {
			return nil, fmt.Errorf("read queue snapshot total state counts: %w", err)
		}
		states := make([]string, 0, len(stateCounts))
		for state := range stateCounts {
			states = append(states, state)
		}
		sort.Strings(states)
		for _, state := range states {
			metadata["state.total."+state] = strconv.Itoa(stateCounts[state])
		}
	}

	return metadata, nil
}

func (s *watchSupervisor) queueSnapshotSourcePresets() map[string][]string {
	presetsBySource := map[string][]string{}
	for _, source := range s.cfg.Watch.Sources {
		sourceName := watchRuntimeSourceName(source)
		if sourceName == "" {
			sourceName = strings.TrimSpace(source.Name)
		}
		if sourceName == "" {
			continue
		}
		presets := presetsBySource[sourceName]
		if strings.TrimSpace(source.Preset) != "" {
			presets = append(presets, source.Preset)
		}
		presetsBySource[sourceName] = presets
	}
	for _, pool := range s.cfg.Watch.RunnerPools {
		sourceName := watchRuntimeSourceNameForConfigSource(s.cfg.Watch.Sources, pool.Source)
		if sourceName == "" {
			sourceName = strings.TrimSpace(pool.Source)
		}
		if sourceName == "" {
			continue
		}
		presetsBySource[sourceName] = append(presetsBySource[sourceName], pool.Presets...)
	}
	for sourceName, presets := range presetsBySource {
		presetsBySource[sourceName] = normalizeRunnerPresets(presets)
	}
	return presetsBySource
}

func watchRuntimeSourceNameForConfigSource(sources []watchSourceConfig, configSource string) string {
	configSource = strings.TrimSpace(configSource)
	for _, source := range sources {
		if strings.TrimSpace(source.Name) == configSource {
			return watchRuntimeSourceName(source)
		}
	}
	return ""
}

func (s *watchSupervisor) reconcilePool(ctx context.Context, state *watchRunnerPoolState) error {
	if state == nil {
		return nil
	}
	sourceName := state.sourceName
	if strings.TrimSpace(sourceName) == "" {
		sourceName = state.pool.Source
	}
	depth, err := s.cfg.QueueDepth.PendingDepth(ctx, sourceName, state.pool.Presets)
	if err != nil {
		return fmt.Errorf("read queue depth for watch runner pool %q: %w", state.pool.Name, err)
	}
	activeDepth, err := s.activeDepth(ctx, sourceName, state.pool.Presets)
	if err != nil {
		return fmt.Errorf("read active queue depth for watch runner pool %q: %w", state.pool.Name, err)
	}

	now := s.cfg.Now()
	desired := desiredWatchRunnerReplicas(state.pool, depth)
	if depth > 0 || activeDepth > 0 {
		state.idleSince = time.Time{}
		if activeDepth > 0 && desired < len(state.runners) {
			desired = len(state.runners)
		}
	} else if len(state.runners) > watchPoolMinReplicas(state.pool) {
		if state.idleSince.IsZero() {
			state.idleSince = now
			desired = len(state.runners)
		} else if now.Sub(state.idleSince) < s.cfg.IdleCooldown {
			desired = len(state.runners)
		}
	} else {
		state.idleSince = time.Time{}
	}

	if desired > len(state.runners) {
		return s.scalePoolUp(ctx, state, desired)
	}
	if desired < len(state.runners) {
		return scaleWatchPoolDown(state, desired)
	}
	return nil
}

func (s *watchSupervisor) activeDepth(ctx context.Context, source string, presets []string) (int, error) {
	provider, ok := s.cfg.QueueDepth.(watchActiveDepthProvider)
	if !ok {
		return 0, nil
	}
	return provider.ActiveDepth(ctx, source, presets)
}

func (s *watchSupervisor) scalePoolUp(ctx context.Context, state *watchRunnerPoolState, desired int) error {
	for len(state.runners) < desired {
		replica := state.nextReplica
		state.nextReplica++
		handle, err := s.cfg.RunnerStarter.StartRunner(ctx, state.pool, replica)
		if err != nil {
			return fmt.Errorf("start watch runner pool %q replica %d: %w", state.pool.Name, replica, err)
		}
		state.runners = append(state.runners, watchRunnerReplica{replica: replica, handle: handle})
	}
	return nil
}

type watchRunnerPoolState struct {
	pool        watchRunnerPoolConfig
	sourceName  string
	runners     []watchRunnerReplica
	nextReplica int
	idleSince   time.Time
}

type watchRunnerReplica struct {
	replica int
	handle  watchManagedProcess
}

func desiredWatchRunnerReplicas(pool watchRunnerPoolConfig, depth int) int {
	if depth < 0 {
		depth = 0
	}
	capacity := watchPoolCapacity(pool)
	desired := watchPoolMinReplicas(pool)
	if depth > 0 {
		desired = (depth + capacity - 1) / capacity
	}
	if minReplicas := watchPoolMinReplicas(pool); desired < minReplicas {
		desired = minReplicas
	}
	if maxReplicas := watchPoolMaxReplicas(pool); desired > maxReplicas {
		desired = maxReplicas
	}
	return desired
}

func normalizeWatchRunnerPool(pool watchRunnerPoolConfig) watchRunnerPoolConfig {
	pool.Presets = normalizeRunnerPresets(pool.Presets)
	minReplicas := watchPoolMinReplicas(pool)
	maxReplicas := watchPoolMaxReplicas(pool)
	capacity := watchPoolCapacity(pool)
	pool.MinReplicas = minReplicas
	pool.MaxReplicas = maxReplicas
	pool.Capacity = capacity
	pool.MinCapacity = minReplicas
	pool.MaxCapacity = maxReplicas
	return pool
}

func watchPoolMinReplicas(pool watchRunnerPoolConfig) int {
	if pool.MinReplicas > 0 {
		return pool.MinReplicas
	}
	if pool.MinCapacity > 0 {
		return pool.MinCapacity
	}
	return defaultWatchRunnerPoolMinCapacity
}

func watchPoolMaxReplicas(pool watchRunnerPoolConfig) int {
	if pool.MaxReplicas > 0 {
		return pool.MaxReplicas
	}
	if pool.MaxCapacity > 0 {
		return pool.MaxCapacity
	}
	return defaultWatchRunnerPoolMaxCapacity
}

func watchPoolCapacity(pool watchRunnerPoolConfig) int {
	if pool.Capacity > 0 {
		return pool.Capacity
	}
	return 1
}

func scaleWatchPoolDown(state *watchRunnerPoolState, desired int) error {
	var err error
	for len(state.runners) > desired {
		last := len(state.runners) - 1
		replica := state.runners[last]
		state.runners = state.runners[:last]
		err = errors.Join(err, replica.handle.Stop())
	}
	return err
}

func stopWatchPools(states map[string]*watchRunnerPoolState) error {
	names := make([]string, 0, len(states))
	for name := range states {
		names = append(names, name)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))

	var err error
	for _, name := range names {
		err = errors.Join(err, scaleWatchPoolDown(states[name], 0))
	}
	return err
}

func stopWatchProcesses(processes []watchManagedProcess) error {
	var err error
	for i := len(processes) - 1; i >= 0; i-- {
		if processes[i] == nil {
			continue
		}
		err = errors.Join(err, processes[i].Stop())
	}
	return err
}

type watchQueueDepthStore struct {
	store *workqueue.Store
}

func (q watchQueueDepthStore) PendingDepth(ctx context.Context, source string, presets []string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if q.store == nil {
		return 0, errors.New("watch queue store is required")
	}
	return q.store.PendingDepth(source, presets)
}

func (q watchQueueDepthStore) ActiveDepth(ctx context.Context, source string, presets []string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if q.store == nil {
		return 0, errors.New("watch queue store is required")
	}
	return q.store.ActiveDepth(source, presets)
}

func (q watchQueueDepthStore) ListSources(ctx context.Context) ([]workqueue.SourceRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if q.store == nil {
		return nil, errors.New("watch queue store is required")
	}
	return q.store.ListSources()
}

func (q watchQueueDepthStore) ItemStateCounts(ctx context.Context) (map[string]int, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if q.store == nil {
		return nil, errors.New("watch queue store is required")
	}
	return q.store.ItemStateCounts()
}

type defaultWatchSourceStarter struct {
	repoRoot  string
	eventSink contracts.EventSink
}

func (s defaultWatchSourceStarter) StartSource(ctx context.Context, source watchSourceConfig, queuePath string) (watchManagedProcess, error) {
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	switch source.Type {
	case watchSourceStartrek:
		go func() {
			done <- defaultRunSourceStartrek(runCtx, sourceStartrekCommandConfig{
				repoRoot:  s.repoRoot,
				profile:   source.Profile,
				queuePath: queuePath,
				eventSink: s.eventSink,
			})
		}()
	case watchSourceBR:
		sourceRepo := strings.TrimSpace(source.Repo)
		if sourceRepo == "" {
			sourceRepo = strings.TrimSpace(s.repoRoot)
		}
		if sourceRepo == "" {
			sourceRepo = "."
		}
		if err := validateBRWorkspace(sourceRepo); err != nil {
			cancel()
			return nil, fmt.Errorf("br source %q watch.sources[].repo %q must contain a .beads workspace; run br init in that repo: %w", source.Name, sourceRepo, err)
		}
		go func() {
			done <- defaultRunSourceBR(runCtx, sourceBRCommandConfig{
				repoRoot:   sourceRepo,
				sourceName: source.Name,
				queuePath:  queuePath,
				preset:     source.Preset,
				rootID:     source.Root,
				eventSink:  s.eventSink,
			})
		}()
	case watchSourceArcPR:
		go func() {
			done <- defaultRunSourceArcPR(runCtx, sourceArcPRCommandConfig{
				repoRoot:  s.repoRoot,
				profile:   source.Profile,
				queuePath: queuePath,
				eventSink: s.eventSink,
			})
		}()
	default:
		cancel()
		return nil, fmt.Errorf("unsupported watch source type %q", source.Type)
	}
	return &watchGoProcess{cancel: cancel, done: done}, nil
}

type defaultWatchRunnerStarter struct {
	queuePath        string
	environmentsPath string
	eventSink        contracts.EventSink
}

func (s defaultWatchRunnerStarter) StartRunner(ctx context.Context, pool watchRunnerPoolConfig, replica int) (watchManagedProcess, error) {
	pool = normalizeWatchRunnerPool(pool)
	runnerID := watchRunnerID(pool.Name, replica)
	daemonCfg, err := normalizeRunnerDaemonConfig(runnerDaemonCommandConfig{
		queuePath:         s.queuePath,
		environmentsPath:  s.environmentsPath,
		presets:           pool.Presets,
		runnerID:          runnerID,
		capacity:          pool.Capacity,
		pollInterval:      embeddedQueueRunnerPollInterval,
		heartbeatInterval: embeddedQueueRunnerHeartbeatInterval,
		leaseTTL:          embeddedQueueRunnerLeaseTTL,
	})
	if err != nil {
		return nil, err
	}

	store, err := workqueue.Open(daemonCfg.queuePath)
	if err != nil {
		return nil, err
	}
	runners, err := openRunnerRegistry(daemonCfg.queuePath)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	if err := runners.Register(daemonCfg.runnerID, daemonCfg.presets, daemonCfg.capacity); err != nil {
		_ = runners.Close()
		_ = store.Close()
		return nil, err
	}

	environmentPresets, err := loadRunnerEnvironmentPresets(daemonCfg.environmentsPath, daemonCfg.presets)
	if err != nil {
		_ = unregisterQueueRunner(runners, daemonCfg.runnerID)
		_ = runners.Close()
		_ = store.Close()
		return nil, err
	}
	runnerSink := watchRunnerEventSink(daemonCfg.runnerID, s.eventSink)
	handlers := defaultRunnerKindRegistry()
	handlers[workitem.KindImplement] = newRunnerImplementKindHandler(newRunnerImplementExecutorResolverForPresets(environmentPresets, runnerSink))
	handlers[workitem.KindSplit] = newRunnerSplitKindHandler(newRunnerSplitAgentResolverForPresets(environmentPresets))
	daemon, err := newRunnerDaemon(daemonCfg, store, runners, runnerDaemonBuildOptions{
		handlers:           handlers,
		environmentPresets: environmentPresets,
		materializer:       envpreset.MaterializeWorkspace,
		eventSink:          runnerSink,
	})
	if err != nil {
		_ = unregisterQueueRunner(runners, daemonCfg.runnerID)
		_ = runners.Close()
		_ = store.Close()
		return nil, err
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		runErr := daemon.Run(runCtx)
		if cleanupErr := unregisterQueueRunner(runners, daemonCfg.runnerID); cleanupErr != nil && (runErr == nil || errors.Is(runErr, context.Canceled)) {
			runErr = cleanupErr
		}
		if closeErr := runners.Close(); closeErr != nil && (runErr == nil || errors.Is(runErr, context.Canceled)) {
			runErr = closeErr
		}
		if closeErr := store.Close(); closeErr != nil && (runErr == nil || errors.Is(runErr, context.Canceled)) {
			runErr = closeErr
		}
		done <- runErr
	}()
	return &watchGoProcess{cancel: cancel, done: done}, nil
}

type watchGoProcess struct {
	cancel context.CancelFunc
	done   <-chan error

	wait    sync.Once
	waitErr error
}

func (p *watchGoProcess) Stop() error {
	if p == nil {
		return nil
	}
	if p.cancel != nil {
		p.cancel()
	}
	err := p.Wait()
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (p *watchGoProcess) Wait() error {
	if p == nil {
		return nil
	}
	p.wait.Do(func() {
		p.waitErr = <-p.done
	})
	return p.waitErr
}

func watchRuntimeSourceName(source watchSourceConfig) string {
	switch source.Type {
	case watchSourceStartrek:
		return sourceStartrekSourceName(source.Profile)
	case watchSourceArcPR:
		return sourceArcPRSourceName(source.Profile)
	default:
		return strings.TrimSpace(source.Name)
	}
}

func watchRunnerID(poolName string, replica int) string {
	return fmt.Sprintf("watch-pool-%s-pid-%d-replica-%d", safeRunnerIDForPath(poolName), os.Getpid(), replica)
}

func watchRunnerEventSink(runnerID string, sink contracts.EventSink) contracts.EventSink {
	fileSink := defaultRunnerDaemonEventSink(runnerID)
	if sink == nil {
		return fileSink
	}
	if fileSink == nil {
		return sink
	}
	return contracts.NewFanoutEventSink(sink, fileSink)
}
