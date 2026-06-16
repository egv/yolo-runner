package main

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
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
	t.Cleanup(func() {
		newWatchConfigService = originalService
		launchYoloTUI = originalLaunch
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

func TestDefaultRunWatchLaunchesTUIWhenConfigDefaultModeIsUI(t *testing.T) {
	originalService := newWatchConfigService
	originalLaunch := launchYoloTUI
	t.Cleanup(func() {
		newWatchConfigService = originalService
		launchYoloTUI = originalLaunch
	})

	queuePath := filepath.Join(t.TempDir(), "watch.db")
	newWatchConfigService = func() watchConfigResolver {
		return staticWatchConfigResolver{cfg: watchConfig{
			QueuePath:   queuePath,
			DefaultMode: agentModeUI,
		}}
	}

	launched := false
	launchYoloTUI = func() (io.WriteCloser, func() error, error) {
		launched = true
		writer := nopWatchWriteCloser{}
		return writer, func() error { return writer.Close() }, nil
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
		t.Fatalf("expected watch default_mode ui to launch yolo-tui")
	}
}

func TestDefaultRunWatchLaunchesTUIWhenFlagIsSet(t *testing.T) {
	originalService := newWatchConfigService
	originalLaunch := launchYoloTUI
	t.Cleanup(func() {
		newWatchConfigService = originalService
		launchYoloTUI = originalLaunch
	})

	queuePath := filepath.Join(t.TempDir(), "watch.db")
	newWatchConfigService = func() watchConfigResolver {
		return staticWatchConfigResolver{cfg: watchConfig{
			QueuePath:   queuePath,
			DefaultMode: agentModeStream,
		}}
	}

	launched := false
	launchYoloTUI = func() (io.WriteCloser, func() error, error) {
		launched = true
		writer := nopWatchWriteCloser{}
		return writer, func() error { return writer.Close() }, nil
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
		t.Fatalf("expected watch tui flag to launch yolo-tui")
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
