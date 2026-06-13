package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/agent"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/engine"
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
	if ctx == nil {
		ctx = context.Background()
	}
	cfg.repoRoot = strings.TrimSpace(cfg.repoRoot)
	if cfg.repoRoot == "" {
		cfg.repoRoot = "."
	}
	cfg.profile = strings.TrimSpace(cfg.profile)
	if cfg.profile == "" {
		return errors.New("--profile is required")
	}

	configService := newSourceStartrekConfigService()
	if configService == nil {
		return errors.New("source startrek config service is required")
	}
	profile, err := configService.ResolveTrackerProfile(cfg.repoRoot, cfg.profile, "", os.Getenv)
	if err != nil {
		return err
	}
	if profile.Tracker.Type != trackerTypeStartrek {
		return fmt.Errorf("profile %q uses tracker type %q, want startrek", profile.Name, profile.Tracker.Type)
	}
	if profile.Tracker.Startrek == nil {
		return errors.New("tracker.startrek settings are required")
	}
	trackerAgentConfig, err := configService.ResolveTrackerAgentConfig(cfg.repoRoot)
	if err != nil {
		return err
	}
	backend, err := buildTrackerWatchStartrekBackend(profile, trackerAgentConfig)
	if err != nil {
		return err
	}

	store, err := workqueue.Open(cfg.queuePath)
	if err != nil {
		return err
	}
	defer store.Close()

	state, err := startreksource.OpenState(sourceStartrekStatePath(cfg.repoRoot, cfg.profile))
	if err != nil {
		return err
	}
	defer state.Close()

	cfg.eventsPath = resolveSourceStartrekEventsPath(cfg)
	eventSink, closeEventSink := watchEventSink(cfg.stream, cfg.eventsPath)
	defer closeEventSink()
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
			Tracker:         backend,
			State:           state,
			Queue:           store,
			ReadyLabel:      trackerAgentConfig.Labels.Ready,
			ProcessingLabel: trackerAgentConfig.Labels.InProgress,
			NeedsInfoLabel:  trackerWatchStartrekNeedsInfoLabel,
			Marker:          trackerWatchStartrekNeedsInfoMarker,
			SubtaskLabel:    "agent:subtask",
			SplitVersion:    trackerWatchStartrekSplitVersion,
		},
		Backend: backend,
		Queues:  profile.Tracker.Startrek.Queues,
		Preset:  cfg.profile,
	}
	return sourcehost.Run(ctx, source, store, sourcehost.Options{
		Once:         cfg.once,
		PollInterval: trackerAgentConfig.PollInterval,
		LockPath:     sourceStartrekLockPath(cfg.repoRoot, cfg.profile),
		EventSink:    eventSink,
	})
}

func (s *sourceStartrekRuntimeSource) Poll(ctx context.Context) ([]workqueue.Submission, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.Source == nil {
		return nil, errors.New("startrek source is required")
	}
	if s.Backend == nil {
		return nil, errors.New("startrek source backend is required")
	}
	preset := strings.TrimSpace(s.Preset)
	if preset == "" {
		return nil, errors.New("startrek source preset is required")
	}

	taskEngine := s.Engine
	if taskEngine == nil {
		taskEngine = engine.NewTaskEngine()
	}

	submissions := make([]workqueue.Submission, 0)
	for _, queue := range s.Queues {
		queueKey := strings.TrimSpace(queue.Key)
		if queueKey == "" {
			continue
		}
		if _, err := s.Backend.ResumeNeedsInfoTasks(ctx, startrek.NeedsInfoResumeInput{
			QueueKey:       queueKey,
			ReadyLabel:     s.ReadyLabel,
			NeedsInfoLabel: s.NeedsInfoLabel,
			Marker:         s.Marker,
		}); err != nil {
			return nil, err
		}

		tree, err := s.Backend.GetTaskTree(ctx, queueKey)
		if err != nil {
			return nil, err
		}
		graph, err := taskEngine.BuildGraph(tree)
		if err != nil {
			return nil, err
		}
		available := taskEngine.GetNextAvailable(graph)
		parentCache := map[string]contracts.Task{}
		for _, summary := range available {
			if strings.EqualFold(strings.TrimSpace(summary.ID), strings.TrimSpace(tree.Root.ID)) {
				continue
			}
			task := startreksource.TrackerWatchStartrekTaskFromTree(summary, tree.Tasks)
			queueRoot, err := trackerWatchStartrekPreflightQueueRoot(ctx, s.Backend, tree.Root, task, tree.Tasks, parentCache)
			if err != nil {
				return nil, err
			}
			submission, err := s.preflightSubmission(task, queueRoot, summary.Priority)
			if err != nil {
				return nil, err
			}
			submissions = append(submissions, submission)
		}
	}
	return submissions, nil
}

func (s *sourceStartrekRuntimeSource) preflightSubmission(task contracts.Task, queueRoot contracts.Task, graphPriority *int) (workqueue.Submission, error) {
	taskID := strings.TrimSpace(task.ID)
	if taskID == "" {
		return workqueue.Submission{}, errors.New("startrek preflight task id is required")
	}
	payload, err := json.Marshal(workitem.PreflightPayload{
		Task:      workitem.TaskPayloadFromTask(task),
		QueueRoot: workitem.TaskPayloadFromTask(queueRoot),
	})
	if err != nil {
		return workqueue.Submission{}, fmt.Errorf("encode startrek preflight payload for issue %q: %w", taskID, err)
	}

	priority := s.Priority
	if graphPriority != nil {
		priority = *graphPriority
	}
	return workqueue.Submission{
		Kind:           workitem.KindPreflight,
		Source:         s.Name(),
		SourceRef:      taskID,
		IdempotencyKey: "st/" + taskID + "/preflight/" + sourceStartrekPreflightRevision(task),
		Preset:         strings.TrimSpace(s.Preset),
		Priority:       priority,
		Payload:        payload,
		MaxAttempts:    s.MaxAttempts,
	}, nil
}

func sourceStartrekPreflightRevision(task contracts.Task) string {
	if task.Metadata != nil {
		for _, key := range []string{"revision", "updated_at", "updatedAt", "updated"} {
			if value := safeStartrekKeyPart(task.Metadata[key]); value != "" {
				return value
			}
		}
	}

	var b strings.Builder
	b.WriteString(strings.TrimSpace(task.ID))
	b.WriteByte('\n')
	b.WriteString(strings.TrimSpace(task.Title))
	b.WriteByte('\n')
	b.WriteString(task.Description)
	b.WriteByte('\n')
	b.WriteString(strings.TrimSpace(task.ParentID))
	if len(task.Metadata) > 0 {
		keys := make([]string, 0, len(task.Metadata))
		for key := range task.Metadata {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			b.WriteByte('\n')
			b.WriteString(strings.TrimSpace(key))
			b.WriteByte('=')
			b.WriteString(strings.TrimSpace(task.Metadata[key]))
		}
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

func safeStartrekKeyPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.' || r == ':':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func resolveSourceStartrekEventsPath(cfg sourceStartrekCommandConfig) string {
	return resolveWatchEventsPath(cfg.eventsPath, cfg.stream, cfg.repoRoot, "source-startrek.events.jsonl")
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
