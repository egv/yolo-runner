package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/agent"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/sourcehost"
	startreksource "github.com/egv/yolo-runner/v2/internal/sources/startrek"
	"github.com/egv/yolo-runner/v2/internal/startrek"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

type sourceStartrekCommandConfig struct {
	repoRoot   string
	profile    string
	queuePath  string
	once       bool
	stream     bool
	eventsPath string
	eventSink  contracts.EventSink
}

type sourceStartrekConfigResolver interface {
	ResolveTrackerAgentConfig(repoRoot string) (trackerAgentConfig, error)
	ResolveTrackerProfile(repoRoot string, selectedProfile string, rootID string, getenv func(string) string) (resolvedTrackerProfile, error)
}

type sourceStartrekBackend interface {
	trackerWatchStartrekBackend
	ResumeNeedsInfoTasks(ctx context.Context, input startrek.NeedsInfoResumeInput) ([]string, error)
	GetTaskTreeForQueue(ctx context.Context, opts startrek.QueueSearchOptions) (*contracts.TaskTree, error)
}

type sourceStartrekRuntimeSource struct {
	*startreksource.Source
	Backend     sourceStartrekBackend
	Queues      []startrekQueueModel
	Preset      string
	Priority    int
	MaxAttempts int
	Engine      contracts.TaskEngine
}

var runSourceStartrek = defaultRunSourceStartrek
var newSourceStartrekRunBundle = buildSourceStartrekRunBundle

var newSourceStartrekConfigService = func() sourceStartrekConfigResolver {
	return newTrackerConfigService()
}

func sourceStartrekCommand(args []string) int {
	fs := flag.NewFlagSet("yolo-agent source startrek", flag.ContinueOnError)
	repo := fs.String("repo", ".", "Repository root")
	profile := fs.String("profile", "", "Startrek source profile and queue preset name")
	queue := fs.String("queue", "", "Path to the SQLite work queue database")
	once := fs.Bool("once", false, "Run one Startrek source iteration and exit")
	stream := fs.Bool("stream", false, "Emit NDJSON events to stdout for piping into yolo-tui")
	events := fs.String("events", "", "Path to JSONL events log")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected source startrek argument: %s\n", fs.Arg(0))
		return 1
	}

	handler := runSourceStartrek
	if handler == nil {
		handler = defaultRunSourceStartrek
	}
	if err := handler(context.Background(), sourceStartrekCommandConfig{
		repoRoot:   *repo,
		profile:    *profile,
		queuePath:  *queue,
		once:       *once,
		stream:     *stream,
		eventsPath: *events,
	}); err != nil {
		fmt.Fprintln(os.Stderr, agent.FormatActionableError(err))
		return 1
	}
	return 0
}

func defaultRunSourceStartrek(ctx context.Context, cfg sourceStartrekCommandConfig) error {
	bundle, err := newSourceStartrekRunBundle(ctx, cfg)
	if err != nil {
		return err
	}
	defer bundle.Close()

	return sourcehost.Run(ctx, bundle.Source, bundle.Store, bundle.Options)
}

type sourceStartrekRunBundle struct {
	Source  *sourceStartrekRuntimeSource
	Store   *workqueue.Store
	Options sourcehost.Options
	closeFn func()
}

func (b sourceStartrekRunBundle) Close() {
	if b.closeFn != nil {
		b.closeFn()
	}
}

func buildSourceStartrekRunBundle(ctx context.Context, cfg sourceStartrekCommandConfig) (sourceStartrekRunBundle, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	cfg.repoRoot = strings.TrimSpace(cfg.repoRoot)
	if cfg.repoRoot == "" {
		cfg.repoRoot = "."
	}
	cfg.profile = strings.TrimSpace(cfg.profile)
	if cfg.profile == "" {
		return sourceStartrekRunBundle{}, errors.New("--profile is required")
	}

	configService := newSourceStartrekConfigService()
	if configService == nil {
		return sourceStartrekRunBundle{}, errors.New("source startrek config service is required")
	}
	profile, err := configService.ResolveTrackerProfile(cfg.repoRoot, cfg.profile, "", os.Getenv)
	if err != nil {
		return sourceStartrekRunBundle{}, err
	}
	if profile.Tracker.Type != trackerTypeStartrek {
		return sourceStartrekRunBundle{}, fmt.Errorf("profile %q uses tracker type %q, want startrek", profile.Name, profile.Tracker.Type)
	}
	if profile.Tracker.Startrek == nil {
		return sourceStartrekRunBundle{}, errors.New("tracker.startrek settings are required")
	}
	trackerAgentConfig, err := configService.ResolveTrackerAgentConfig(cfg.repoRoot)
	if err != nil {
		return sourceStartrekRunBundle{}, err
	}
	backend, err := buildTrackerWatchStartrekBackend(profile, trackerAgentConfig)
	if err != nil {
		return sourceStartrekRunBundle{}, err
	}

	store, err := workqueue.Open(cfg.queuePath)
	if err != nil {
		return sourceStartrekRunBundle{}, err
	}

	state, err := startreksource.OpenState(sourceStartrekStatePath(cfg.repoRoot, cfg.profile))
	if err != nil {
		_ = store.Close()
		return sourceStartrekRunBundle{}, err
	}

	eventSink, closeEventSink := watchEventSink(cfg.stream, "")
	if cfg.eventSink != nil {
		if eventSink != nil {
			eventSink = contracts.NewFanoutEventSink(cfg.eventSink, eventSink)
		} else {
			eventSink = cfg.eventSink
		}
	}

	sourceName := sourceStartrekSourceName(cfg.profile)
	source := &sourceStartrekRuntimeSource{
		Source: &startreksource.Source{
			SourceName:      sourceName,
			Backend:         backend,
			Tracker:         backend,
			State:           state,
			Queue:           store,
			Queues:          sourceStartrekQueues(profile.Tracker.Startrek.Queues),
			Preset:          cfg.profile,
			ReadyLabel:      trackerAgentConfig.Labels.Ready,
			ProcessingLabel: trackerWatchStartrekProcessingLabel,
			NeedsInfoLabel:  trackerWatchStartrekNeedsInfoLabel,
			Marker:          trackerWatchStartrekNeedsInfoMarker,
			SplitVersion:    trackerWatchStartrekSplitVersion,
		},
		Backend: backend,
		Queues:  profile.Tracker.Startrek.Queues,
		Preset:  cfg.profile,
	}

	return sourceStartrekRunBundle{
		Source: source,
		Store:  store,
		Options: sourcehost.Options{
			Once:         cfg.once,
			PollInterval: trackerAgentConfig.PollInterval,
			LockPath:     sourceStartrekLockPath(cfg.repoRoot, cfg.profile),
			EventsPath:   cfg.eventsPath,
			EventSink:    eventSink,
		},
		closeFn: func() {
			closeEventSink()
			_ = state.Close()
			_ = store.Close()
		},
	}, nil
}

func (s *sourceStartrekRuntimeSource) Poll(ctx context.Context) ([]workqueue.Submission, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.Source == nil {
		return nil, errors.New("startrek source is required")
	}
	if s.Backend != nil {
		s.Source.Backend = s.Backend
	}
	if len(s.Source.Queues) == 0 {
		s.Source.Queues = sourceStartrekQueues(s.Queues)
	}
	if strings.TrimSpace(s.Source.Preset) == "" {
		s.Source.Preset = s.Preset
	}
	if s.Source.Priority == 0 {
		s.Source.Priority = s.Priority
	}
	if s.Source.MaxAttempts == 0 {
		s.Source.MaxAttempts = s.MaxAttempts
	}
	if s.Source.Engine == nil {
		s.Source.Engine = s.Engine
	}
	return s.Source.Poll(ctx)
}

func (s *sourceStartrekRuntimeSource) HandleResult(ctx context.Context, item workitem.Item, result workqueue.Result) ([]workqueue.Submission, error) {
	if s == nil || s.Source == nil {
		return nil, errors.New("startrek source is required")
	}
	if err := s.clearReadyLabelForBlockingPreflightResult(ctx, item, result); err != nil {
		return nil, err
	}
	return s.Source.HandleResult(ctx, item, result)
}

func (s *sourceStartrekRuntimeSource) clearReadyLabelForBlockingPreflightResult(ctx context.Context, item workitem.Item, result workqueue.Result) error {
	if item.Kind != workitem.KindPreflight {
		return nil
	}
	if result.Status != "" && result.Status != workqueue.ResultStatusCompleted {
		return nil
	}
	var preflightResult workitem.PreflightResult
	if err := json.Unmarshal(result.Payload, &preflightResult); err != nil {
		return fmt.Errorf("decode startrek preflight result for ready-label cleanup on item %q: %w", item.ID, err)
	}
	switch preflightResult.Verdict {
	case workitem.PreflightVerdictNeedsInfo, workitem.PreflightVerdictReply:
	default:
		return nil
	}
	issueID := strings.TrimSpace(item.SourceRef)
	if issueID == "" {
		return nil
	}
	if s.Backend == nil {
		return errors.New("startrek source backend is required")
	}
	readyLabel := strings.TrimSpace(s.ReadyLabel)
	if readyLabel == "" {
		readyLabel = defaultTrackerAgentReadyLabel
	}
	if err := s.Backend.RemoveLabel(ctx, issueID, readyLabel); err != nil {
		return fmt.Errorf("remove startrek ready label from blocking preflight issue %q: %w", issueID, err)
	}
	return nil
}

func resolveSourceStartrekEventsPath(cfg sourceStartrekCommandConfig) string {
	return resolveSourceEventsPath(cfg.eventsPath, sourceStartrekSourceName(cfg.profile))
}

func sourceStartrekQueues(queues []startrekQueueModel) []startreksource.Queue {
	out := make([]startreksource.Queue, 0, len(queues))
	for _, queue := range queues {
		out = append(out, startreksource.Queue{
			Key:      queue.Key,
			Preset:   queue.Preset,
			Assignee: queue.Assignee,
			Label:    queue.Label,
		})
	}
	return out
}

func sourceStartrekStatePath(repoRoot string, profile string) string {
	return filepath.Join(repoRoot, ".yolo-runner", "sources", sourceStartrekSourceName(profile)+".db")
}

func sourceStartrekLockPath(repoRoot string, profile string) string {
	return filepath.Join(repoRoot, ".yolo-runner", sourceStartrekSourceName(profile)+".lock")
}

func sourceStartrekSourceName(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return "startrek"
	}
	return "startrek-" + profile
}
