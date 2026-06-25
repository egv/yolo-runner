package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/agent"
	"github.com/egv/yolo-runner/v2/internal/arcanum"
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
var newSourceArcPRAPIClient = func() (*arcanum.APIClient, error) {
	return arcanum.NewAPIClient(arcanum.APIClientConfig{
		BaseURL: arcanum.DefaultAPIBaseURL,
	})
}

// Applier seams are nil in production: the arcpr source then builds real
// Arcanum-backed appliers from the default API client. Tests inject fakes so
// result consumption does not reach the live Arcanum API.
var sourceArcPRReplyApplier arcreview.PRReviewCycleReplyApplier
var sourceArcPRReviewApplier arcreview.PRReviewCycleReviewApplier
var sourceArcPRShipGate arcreview.PRReviewCycleShipGate
var newSourceArcPRRunBundle = buildSourceArcPRRunBundle

var newSourceArcPRConfigService = func() arcReviewWatchConfigResolver {
	return newTrackerConfigService()
}

func sourceCommand(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yolo-agent source <arcpr|br|startrek> [flags]")
		return 1
	}

	switch args[0] {
	case "arcpr":
		return sourceArcPRCommand(args[1:])
	case "br":
		return sourceBRCommand(args[1:])
	case "startrek":
		return sourceStartrekCommand(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown source command: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "usage: yolo-agent source <arcpr|br|startrek> [flags]")
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
	bundle, err := newSourceArcPRRunBundle(ctx, cfg)
	if err != nil {
		return err
	}
	defer bundle.Close()

	return sourcehost.Run(ctx, bundle.Source, bundle.Store, bundle.Options)
}

type sourceArcPRRunBundle struct {
	Source  *arcpr.Source
	Store   *workqueue.Store
	Options sourcehost.Options
	closeFn func()
}

func (b sourceArcPRRunBundle) Close() {
	if b.closeFn != nil {
		b.closeFn()
	}
}

func buildSourceArcPRRunBundle(ctx context.Context, cfg sourceArcPRCommandConfig) (sourceArcPRRunBundle, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	cfg.repoRoot = strings.TrimSpace(cfg.repoRoot)
	if cfg.repoRoot == "" {
		cfg.repoRoot = "."
	}
	cfg.profile = strings.TrimSpace(cfg.profile)
	if cfg.profile == "" {
		return sourceArcPRRunBundle{}, errors.New("--profile is required")
	}

	configService := newSourceArcPRConfigService()
	if configService == nil {
		return sourceArcPRRunBundle{}, errors.New("source arcpr config service is required")
	}
	reviewWatchConfig, err := configService.ResolveArcReviewWatchConfig(cfg.repoRoot)
	if err != nil {
		return sourceArcPRRunBundle{}, err
	}

	store, err := workqueue.Open(cfg.queuePath)
	if err != nil {
		return sourceArcPRRunBundle{}, err
	}

	state, err := arcreviewstate.Open(reviewWatchConfig.StatePath)
	if err != nil {
		_ = store.Close()
		return sourceArcPRRunBundle{}, err
	}

	eventSink, closeEventSink := watchEventSink(cfg.stream, "")
	if cfg.eventSink != nil {
		if eventSink != nil {
			eventSink = contracts.NewFanoutEventSink(cfg.eventSink, eventSink)
		} else {
			eventSink = cfg.eventSink
		}
	}

	apiClient, err := newSourceArcPRAPIClient()
	if err != nil {
		_ = state.Close()
		_ = store.Close()
		return sourceArcPRRunBundle{}, fmt.Errorf("build Arcanum API client: %w", err)
	}

	// NewSource seeds the author-mode gates (default true); cmd then wires the
	// connection-scoped fields plus the orchestration Queue handle.
	source := arcpr.NewSource()
	source.SourceName = sourceArcPRSourceName(cfg.profile)
	source.Preset = cfg.profile
	source.Reviewer = reviewWatchConfig.Reviewer
	source.ObjectsBaseDir = reviewWatchConfig.ObjectsBaseDir
	source.MountsBaseDir = reviewWatchConfig.MountsBaseDir
	source.AllowShip = reviewWatchConfig.AllowShip
	source.State = state
	source.Lister = sourceArcPRReviewLister()
	source.StateFetcher = sourceArcPRStateFetcher
	source.APIClient = apiClient
	source.ReplyApplier = sourceArcPRReplyApplier
	source.ReviewApplier = sourceArcPRReviewApplier
	source.ShipGate = sourceArcPRShipGate
	source.Queue = store

	return sourceArcPRRunBundle{
		Source: source,
		Store:  store,
		Options: sourcehost.Options{
			Once:         cfg.once,
			PollInterval: reviewWatchConfig.PollInterval,
			LockPath:     reviewWatchConfig.LockPath,
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

func sourceArcPRReviewLister() arcpr.PRLister {
	return sourceArcPRLister
}
