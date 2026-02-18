package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/anomalyco/yolo-runner/internal/linear/webhook"
)

type runConfig struct {
	queuePath    string
	pollInterval time.Duration
	once         bool
}

var processLinearSessionJob = defaultProcessLinearSessionJob

func main() {
	os.Exit(RunMain(os.Args[1:], nil))
}

func RunMain(args []string, run func(context.Context, runConfig) error) int {
	fs := flag.NewFlagSet("yolo-linear-worker", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	queuePath := fs.String("queue-path", filepath.Join("runner-logs", "linear-webhook.jobs.jsonl"), "Queue source JSONL path")
	pollInterval := fs.Duration("poll-interval", 250*time.Millisecond, "Polling interval when watching the queue file")
	once := fs.Bool("once", false, "Drain currently queued jobs and exit")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	if strings.TrimSpace(*queuePath) == "" {
		fmt.Fprintln(os.Stderr, "--queue-path is required")
		return 1
	}
	if *pollInterval <= 0 {
		fmt.Fprintln(os.Stderr, "--poll-interval must be greater than 0")
		return 1
	}

	if run == nil {
		run = defaultRun
	}

	cfg := runConfig{
		queuePath:    *queuePath,
		pollInterval: *pollInterval,
		once:         *once,
	}
	if err := run(context.Background(), cfg); err != nil {
		fmt.Fprintln(os.Stderr, FormatLinearSessionActionableError(err))
		return 1
	}
	return 0
}

func defaultRun(ctx context.Context, cfg runConfig) error {
	worker, err := webhook.NewWorker(webhook.WorkerConfig{
		QueuePath:    cfg.queuePath,
		PollInterval: cfg.pollInterval,
		Once:         cfg.once,
	}, webhook.JobProcessorFunc(processLinearSessionJob))
	if err != nil {
		return err
	}

	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return worker.Run(runCtx)
}
