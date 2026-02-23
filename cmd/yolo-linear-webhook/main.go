package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/egv/yolo-runner/internal/linear/webhook"
)

type runConfig struct {
	listenAddr      string
	webhookPath     string
	queuePath       string
	queueBuffer     int
	shutdownTimeout time.Duration
}

func main() {
	os.Exit(RunMain(os.Args[1:], nil))
}

func RunMain(args []string, run func(context.Context, runConfig) error) int {
	fs := flag.NewFlagSet("yolo-linear-webhook", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	listenAddr := fs.String("listen", ":8080", "HTTP listen address")
	webhookPath := fs.String("path", "/linear/webhook", "Webhook route path")
	queuePath := fs.String("queue-path", filepath.Join("runner-logs", "linear-webhook.jobs.jsonl"), "Queue sink JSONL path")
	queueBuffer := fs.Int("queue-buffer", 128, "In-memory async queue buffer")
	shutdownTimeout := fs.Duration("shutdown-timeout", 5*time.Second, "Graceful shutdown timeout")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	if strings.TrimSpace(*webhookPath) == "" || !strings.HasPrefix(*webhookPath, "/") {
		fmt.Fprintln(os.Stderr, "--path must be an absolute route path, e.g. /linear/webhook")
		return 1
	}
	if strings.TrimSpace(*queuePath) == "" {
		fmt.Fprintln(os.Stderr, "--queue-path is required")
		return 1
	}
	if *queueBuffer <= 0 {
		fmt.Fprintln(os.Stderr, "--queue-buffer must be greater than 0")
		return 1
	}
	if *shutdownTimeout <= 0 {
		fmt.Fprintln(os.Stderr, "--shutdown-timeout must be greater than 0")
		return 1
	}

	if run == nil {
		run = defaultRun
	}

	cfg := runConfig{
		listenAddr:      *listenAddr,
		webhookPath:     *webhookPath,
		queuePath:       *queuePath,
		queueBuffer:     *queueBuffer,
		shutdownTimeout: *shutdownTimeout,
	}
	if err := run(context.Background(), cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func defaultRun(ctx context.Context, cfg runConfig) error {
	queue := webhook.NewJSONLQueue(cfg.queuePath)
	dispatcher := webhook.NewAsyncDispatcher(queue, cfg.queueBuffer)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.shutdownTimeout)
		defer cancel()
		_ = dispatcher.Close(shutdownCtx)
	}()

	mux := http.NewServeMux()
	mux.Handle(cfg.webhookPath, webhook.NewHandler(dispatcher, webhook.HandlerOptions{}))
	server := &http.Server{Addr: cfg.listenAddr, Handler: mux}

	shutdownCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-shutdownCtx.Done()
		ctx, cancel := context.WithTimeout(context.Background(), cfg.shutdownTimeout)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	err := server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
