package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestWatchHelpIncludesTUIFlagAndExitsZero(t *testing.T) {
	helpText := captureStderr(t, func() {
		code := RunMain([]string{"watch", "--help"}, func(context.Context, runConfig) error {
			t.Fatalf("legacy run function should not be called for watch help")
			return nil
		})
		if code != 0 {
			t.Fatalf("expected watch --help exit code 0, got %d", code)
		}
	})

	if !strings.Contains(helpText, "Usage of yolo-agent watch") {
		t.Fatalf("expected watch usage in help output, got %q", helpText)
	}
	if !strings.Contains(helpText, "-tui") {
		t.Fatalf("expected watch help to mention --tui, got %q", helpText)
	}
}

func TestRunMainRoutesWatchTUIFlag(t *testing.T) {
	originalRun := runWatch
	t.Cleanup(func() {
		runWatch = originalRun
	})

	called := false
	runWatch = func(_ context.Context, cfg watchCommandConfig) error {
		called = true
		if !cfg.tui {
			t.Fatalf("expected watch --tui to set tui=true")
		}
		if cfg.stream {
			t.Fatalf("watch --tui should not require stdout stream mode")
		}
		return nil
	}

	code := RunMain([]string{"watch", "--repo", "/repo", "--tui"}, func(context.Context, runConfig) error {
		t.Fatalf("legacy run function should not be called for watch")
		return nil
	})
	if code != 0 {
		t.Fatalf("expected watch --tui exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected watch handler to be called")
	}
}

func TestDefaultRunWatchKeepsHeadlessModeFromLaunchingTUI(t *testing.T) {
	originalService := newWatchConfigService
	originalLaunch := launchYoloTUI
	originalLaunchBoard := launchYoloBoard
	t.Cleanup(func() {
		newWatchConfigService = originalService
		launchYoloTUI = originalLaunch
		launchYoloBoard = originalLaunchBoard
	})

	queuePath := filepath.Join(t.TempDir(), "watch.db")
	newWatchConfigService = func() watchConfigResolver {
		return staticWatchConfigResolver{cfg: watchConfig{
			QueuePath:   queuePath,
			DefaultMode: agentModeStream,
		}}
	}
	launchYoloTUI = func() (io.WriteCloser, func() error, error) {
		t.Fatalf("headless watch mode should not launch yolo-tui")
		return nil, nil, nil
	}
	launchYoloBoard = func(string) (io.WriteCloser, func() error, error) {
		t.Fatalf("headless watch mode should not launch yolo-board")
		return nil, nil, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := defaultRunWatch(ctx, watchCommandConfig{
		repoRoot:     t.TempDir(),
		tickInterval: defaultWatchSupervisorTickInterval,
		idleCooldown: defaultWatchIdleCooldown,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled watch run, got %v", err)
	}
}

func TestDefaultRunWatchLaunchesBoardWhenConfigDefaultModeIsUI(t *testing.T) {
	originalService := newWatchConfigService
	originalLaunchBoard := launchYoloBoard
	originalLaunchTUI := launchYoloTUI
	t.Cleanup(func() {
		newWatchConfigService = originalService
		launchYoloBoard = originalLaunchBoard
		launchYoloTUI = originalLaunchTUI
	})

	queuePath := filepath.Join(t.TempDir(), "watch.db")
	newWatchConfigService = func() watchConfigResolver {
		return staticWatchConfigResolver{cfg: watchConfig{
			QueuePath:   queuePath,
			DefaultMode: agentModeUI,
		}}
	}

	launched := false
	launchYoloBoard = func(string) (io.WriteCloser, func() error, error) {
		launched = true
		writer := nopWatchWriteCloser{}
		return writer, func() error { return writer.Close() }, nil
	}
	launchYoloTUI = func() (io.WriteCloser, func() error, error) {
		t.Fatalf("watch default_mode ui should launch yolo-board, not yolo-tui")
		return nil, nil, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := defaultRunWatch(ctx, watchCommandConfig{
		repoRoot:     t.TempDir(),
		tickInterval: defaultWatchSupervisorTickInterval,
		idleCooldown: defaultWatchIdleCooldown,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled watch run, got %v", err)
	}
	if !launched {
		t.Fatalf("expected watch default_mode ui to launch yolo-board")
	}
}

func TestDefaultRunWatchLaunchesBoardWhenFlagIsSet(t *testing.T) {
	originalService := newWatchConfigService
	originalLaunchBoard := launchYoloBoard
	originalLaunchTUI := launchYoloTUI
	t.Cleanup(func() {
		newWatchConfigService = originalService
		launchYoloBoard = originalLaunchBoard
		launchYoloTUI = originalLaunchTUI
	})

	queuePath := filepath.Join(t.TempDir(), "watch.db")
	newWatchConfigService = func() watchConfigResolver {
		return staticWatchConfigResolver{cfg: watchConfig{
			QueuePath:   queuePath,
			DefaultMode: agentModeStream,
		}}
	}

	launched := false
	launchYoloBoard = func(string) (io.WriteCloser, func() error, error) {
		launched = true
		writer := nopWatchWriteCloser{}
		return writer, func() error { return writer.Close() }, nil
	}
	launchYoloTUI = func() (io.WriteCloser, func() error, error) {
		t.Fatalf("watch --tui should launch yolo-board, not yolo-tui")
		return nil, nil, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := defaultRunWatch(ctx, watchCommandConfig{
		repoRoot:     t.TempDir(),
		tui:          true,
		tickInterval: defaultWatchSupervisorTickInterval,
		idleCooldown: defaultWatchIdleCooldown,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled watch run, got %v", err)
	}
	if !launched {
		t.Fatalf("expected watch tui flag to launch yolo-board")
	}
}

func TestDefaultRunWatchTUILaunchesBoardWithQueuePathAndStreamsEvents(t *testing.T) {
	originalService := newWatchConfigService
	originalLaunchBoard := launchYoloBoard
	originalLaunchTUI := launchYoloTUI
	t.Cleanup(func() {
		newWatchConfigService = originalService
		launchYoloBoard = originalLaunchBoard
		launchYoloTUI = originalLaunchTUI
	})

	queuePath := filepath.Join(t.TempDir(), "watch.db")
	newWatchConfigService = func() watchConfigResolver {
		return staticWatchConfigResolver{cfg: watchConfig{
			QueuePath:   queuePath,
			DefaultMode: agentModeStream,
			RunnerPools: []watchRunnerPoolConfig{
				{Name: "linux-pool", Source: "source-a", Presets: []string{"linux"}, MaxReplicas: 1, Capacity: 1},
			},
		}}
	}

	var launchedQueuePath string
	writer := &bufferWatchWriteCloser{}
	launchYoloBoard = func(queuePath string) (io.WriteCloser, func() error, error) {
		launchedQueuePath = queuePath
		return writer, func() error { return writer.Close() }, nil
	}
	launchYoloTUI = func() (io.WriteCloser, func() error, error) {
		t.Fatalf("watch --tui should launch yolo-board, not yolo-tui")
		return nil, nil, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelSink := &cancelingWatchSink{cancel: cancel}
	err := defaultRunWatch(ctx, watchCommandConfig{
		repoRoot:     t.TempDir(),
		tui:          true,
		tickInterval: defaultWatchSupervisorTickInterval,
		idleCooldown: defaultWatchIdleCooldown,
		eventSink:    cancelSink,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled watch run, got %v", err)
	}
	if launchedQueuePath != queuePath {
		t.Fatalf("launchYoloBoard queue path = %q, want %q", launchedQueuePath, queuePath)
	}
	if cancelSink.events == 0 {
		t.Fatalf("expected watch supervisor to emit at least one event")
	}

	decoder := contracts.NewEventDecoder(bytes.NewReader(writer.Bytes()))
	event, err := decoder.Next()
	if err != nil {
		t.Fatalf("expected event streamed to yolo-board stdin: %v; raw=%q", err, writer.String())
	}
	if event.Type != contracts.EventTypeQueueSnapshot {
		t.Fatalf("streamed event type = %q, want %q", event.Type, contracts.EventTypeQueueSnapshot)
	}
}

type staticWatchConfigResolver struct {
	cfg watchConfig
	err error
}

func (r staticWatchConfigResolver) ResolveWatchConfig(string) (watchConfig, error) {
	return r.cfg, r.err
}

type nopWatchWriteCloser struct{}

func (nopWatchWriteCloser) Write(p []byte) (int, error) {
	return len(p), nil
}

func (nopWatchWriteCloser) Close() error {
	return nil
}

type bufferWatchWriteCloser struct {
	bytes.Buffer
}

func (w *bufferWatchWriteCloser) Close() error {
	return nil
}

type cancelingWatchSink struct {
	cancel context.CancelFunc
	events int
}

func (s *cancelingWatchSink) Emit(context.Context, contracts.Event) error {
	s.events++
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}
