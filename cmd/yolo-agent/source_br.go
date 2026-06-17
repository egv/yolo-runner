package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/agent"
	beadsbackend "github.com/egv/yolo-runner/v2/internal/beads"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/sourcehost"
	beadssource "github.com/egv/yolo-runner/v2/internal/sources/beads"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

type sourceBRCommandConfig struct {
	repoRoot   string
	sourceName string
	queuePath  string
	preset     string
	rootID     string
	once       bool
	stream     bool
	eventsPath string
	eventSink  contracts.EventSink
}

var runSourceBR = defaultRunSourceBR
var newSourceBRRunBundle = buildSourceBRRunBundle

func sourceBRCommand(args []string) int {
	fs := flag.NewFlagSet("yolo-agent source br", flag.ContinueOnError)
	repo := fs.String("repo", ".", "Repository root containing .beads")
	name := fs.String("name", "", "Source name for queued work")
	queue := fs.String("queue", "", "Path to the SQLite work queue database")
	preset := fs.String("preset", "", "Runner preset for queued br tasks")
	root := fs.String("root", "", "Optional br epic/root scope")
	once := fs.Bool("once", false, "Run one br source iteration and exit")
	stream := fs.Bool("stream", false, "Emit NDJSON events to stdout for piping into yolo-tui")
	events := fs.String("events", "", "Path to JSONL events log")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected source br argument: %s\n", fs.Arg(0))
		return 1
	}

	handler := runSourceBR
	if handler == nil {
		handler = defaultRunSourceBR
	}
	if err := handler(context.Background(), sourceBRCommandConfig{
		repoRoot:   *repo,
		sourceName: *name,
		queuePath:  *queue,
		preset:     *preset,
		rootID:     *root,
		once:       *once,
		stream:     *stream,
		eventsPath: *events,
	}); err != nil {
		fmt.Fprintln(os.Stderr, agent.FormatActionableError(err))
		return 1
	}
	return 0
}

func defaultRunSourceBR(ctx context.Context, cfg sourceBRCommandConfig) error {
	bundle, err := newSourceBRRunBundle(ctx, cfg)
	if err != nil {
		return err
	}
	defer bundle.Close()

	return sourcehost.Run(ctx, bundle.Source, bundle.Store, bundle.Options)
}

type sourceBRRunBundle struct {
	Source  sourcehost.Source
	Store   *workqueue.Store
	Options sourcehost.Options
	closeFn func()
}

func (b sourceBRRunBundle) Close() {
	if b.closeFn != nil {
		b.closeFn()
	}
}

func buildSourceBRRunBundle(ctx context.Context, cfg sourceBRCommandConfig) (sourceBRRunBundle, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	cfg.repoRoot = strings.TrimSpace(cfg.repoRoot)
	if cfg.repoRoot == "" {
		cfg.repoRoot = "."
	}
	cfg.sourceName = strings.TrimSpace(cfg.sourceName)
	if cfg.sourceName == "" {
		cfg.sourceName = "br"
	}
	cfg.preset = strings.TrimSpace(cfg.preset)
	if cfg.preset == "" {
		return sourceBRRunBundle{}, errors.New("--preset is required")
	}
	if err := validateBRWorkspace(cfg.repoRoot); err != nil {
		return sourceBRRunBundle{}, err
	}

	store, err := workqueue.Open(cfg.queuePath)
	if err != nil {
		return sourceBRRunBundle{}, err
	}

	eventSink, closeEventSink := watchEventSink(cfg.stream, "")
	if cfg.eventSink != nil {
		if eventSink != nil {
			eventSink = contracts.NewFanoutEventSink(cfg.eventSink, eventSink)
		} else {
			eventSink = cfg.eventSink
		}
	}

	storage := beadsbackend.NewStorageBackend(localRunner{dir: cfg.repoRoot}, cfg.repoRoot)
	source := &beadssource.Source{
		SourceName: cfg.sourceName,
		RootID:     cfg.rootID,
		Preset:     cfg.preset,
		Storage:    storage,
		Queue:      store,
	}

	return sourceBRRunBundle{
		Source: source,
		Store:  store,
		Options: sourcehost.Options{
			Once:       cfg.once,
			LockPath:   sourceBRLockPath(cfg.repoRoot, cfg.sourceName),
			EventsPath: cfg.eventsPath,
			EventSink:  eventSink,
		},
		closeFn: func() {
			closeEventSink()
			_ = store.Close()
		},
	}, nil
}

func validateBRWorkspace(repoRoot string) error {
	beadsDir := filepath.Join(repoRoot, ".beads")
	info, err := os.Stat(beadsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("br workspace not found at %s; run br init in %s", beadsDir, repoRoot)
		}
		return fmt.Errorf("inspect br workspace at %s: %w", beadsDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("br workspace path %s is not a directory", beadsDir)
	}
	return nil
}

func sourceBRLockPath(repoRoot string, sourceName string) string {
	sourceName = strings.TrimSpace(sourceName)
	if sourceName == "" {
		sourceName = "br"
	}
	return filepath.Join(repoRoot, ".yolo-runner", "source-"+safeRunnerIDForPath(sourceName)+".lock")
}
