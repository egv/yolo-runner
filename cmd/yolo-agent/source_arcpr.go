package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/agent"
	"github.com/egv/yolo-runner/v2/internal/arcreview"
	arcreviewstate "github.com/egv/yolo-runner/v2/internal/arcreview/state"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/sourcehost"
	"github.com/egv/yolo-runner/v2/internal/sources/arcpr"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

type sourceArcPRCommandConfig struct {
	repoRoot   string
	profile    string
	queuePath  string
	once       bool
	stream     bool
	eventsPath string
	eventSink  contracts.EventSink
}

var runSourceArcPR = defaultRunSourceArcPR
var sourceArcPRLister arcpr.PRLister
var sourceArcPRStateFetcher arcpr.PRStateFetcher

// Applier seams are nil in production: the arcpr source then builds real
// Arcanum-backed appliers from the default API client. Tests inject fakes so
// result consumption does not reach the live Arcanum API.
var sourceArcPRReplyApplier arcreview.PRReviewCycleReplyApplier
var sourceArcPRReviewApplier arcreview.PRReviewCycleReviewApplier
var sourceArcPRShipGate arcreview.PRReviewCycleShipGate

var newSourceArcPRConfigService = func() arcReviewWatchConfigResolver {
	return newTrackerConfigService()
}

func sourceCommand(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yolo-agent source <arcpr|startrek> [flags]")
		return 1
	}

	switch args[0] {
	case "arcpr":
		return sourceArcPRCommand(args[1:])
	case "startrek":
		return sourceStartrekCommand(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown source command: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "usage: yolo-agent source <arcpr|startrek> [flags]")
		return 1
	}
}

func sourceArcPRCommand(args []string) int {
	fs := flag.NewFlagSet("yolo-agent source arcpr", flag.ContinueOnError)
	repo := fs.String("repo", ".", "Repository root")
	profile := fs.String("profile", "", "Source profile and queue preset name")
	queue := fs.String("queue", "", "Path to the SQLite work queue database")
	once := fs.Bool("once", false, "Run one Arc PR source iteration and exit")
	stream := fs.Bool("stream", false, "Emit NDJSON events to stdout for piping into yolo-tui")
	events := fs.String("events", "", "Path to JSONL events log")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected source arcpr argument: %s\n", fs.Arg(0))
		return 1
	}

	handler := runSourceArcPR
	if handler == nil {
		handler = defaultRunSourceArcPR
	}
	if err := handler(context.Background(), sourceArcPRCommandConfig{
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

func defaultRunSourceArcPR(ctx context.Context, cfg sourceArcPRCommandConfig) error {
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

	configService := newSourceArcPRConfigService()
	if configService == nil {
		return errors.New("source arcpr config service is required")
	}
	reviewWatchConfig, err := configService.ResolveArcReviewWatchConfig(cfg.repoRoot)
	if err != nil {
		return err
	}

	store, err := workqueue.Open(cfg.queuePath)
	if err != nil {
		return err
	}
	defer store.Close()

	state, err := arcreviewstate.Open(reviewWatchConfig.StatePath)
	if err != nil {
		return err
	}
	defer state.Close()

	eventSink, closeEventSink := watchEventSink(cfg.stream, "")
	defer closeEventSink()
	if cfg.eventSink != nil {
		if eventSink != nil {
			eventSink = contracts.NewFanoutEventSink(cfg.eventSink, eventSink)
		} else {
			eventSink = cfg.eventSink
		}
	}

	source := &arcpr.Source{
		SourceName:         sourceArcPRSourceName(cfg.profile),
		Preset:             cfg.profile,
		Reviewer:           reviewWatchConfig.Reviewer,
		WritebackWorkspace: sourceArcPRWritebackWorkspace(reviewWatchConfig.Workspaces),
		AllowShip:          reviewWatchConfig.AllowShip,
		State:              state,
		Lister:             sourceArcPRLister,
		StateFetcher:       sourceArcPRStateFetcher,
		ReplyApplier:       sourceArcPRReplyApplier,
		ReviewApplier:      sourceArcPRReviewApplier,
		ShipGate:           sourceArcPRShipGate,
	}
	return sourcehost.Run(ctx, source, store, sourcehost.Options{
		Once:         cfg.once,
		PollInterval: reviewWatchConfig.PollInterval,
		LockPath:     reviewWatchConfig.LockPath,
		EventsPath:   cfg.eventsPath,
		EventSink:    eventSink,
	})
}

func resolveSourceArcPREventsPath(cfg sourceArcPRCommandConfig) string {
	return resolveSourceEventsPath(cfg.eventsPath, sourceArcPRSourceName(cfg.profile))
}

func sourceArcPRSourceName(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return "arcpr"
	}
	return "arcpr-" + profile
}

func sourceArcPRWritebackWorkspace(workspaces []string) string {
	for _, workspace := range workspaces {
		if workspace := strings.TrimSpace(workspace); workspace != "" {
			return workspace
		}
	}
	return ""
}
