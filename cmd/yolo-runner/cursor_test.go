//go:build legacy

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/internal/runner"
	"github.com/egv/yolo-runner/internal/ui"
)

func TestCursorVisibleAfterProgressOutput(t *testing.T) {
	// Test that progress output finishes with cursor visible
	buffer := &bytes.Buffer{}
	progress := ui.NewProgress(ui.ProgressConfig{
		Writer: buffer,
		State:  "opencode running",
	})

	progress.Finish(nil)

	output := buffer.String()

	// The output should contain cursor show sequence after finishing
	if !strings.Contains(output, "\x1b[?25h") {
		t.Errorf("Progress Finish() should include cursor show sequence (\\x1b[?25h), got: %q", output)
	}

	// Should also have finish message
	if !strings.Contains(output, "OpenCode finished") {
		t.Errorf("Progress Finish() should include finish message, got: %q", output)
	}
}

func TestCursorVisibleAfterRunnerExit(t *testing.T) {
	// Test that cursor is visible after runner exits
	tempDir := t.TempDir()
	writeAgentFile(t, tempDir, "---\npermission: allow\n---\n")

	exit := &fakeExit{}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	// Override isTerminal to simulate TTY
	prevIsTerminal := isTerminal
	isTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() {
		isTerminal = prevIsTerminal
	})

	// Use a runOnce that returns no_tasks to stop immediately
	runOnce := func(opts runner.RunOnceOptions, deps runner.RunOnceDeps) (string, error) {
		// Simulate some progress output
		if opts.Out != nil {
			fmt.Fprint(opts.Out, "some output")
		}
		return "no_tasks", nil
	}

	code := RunOnceMain([]string{"--repo", tempDir, "--root", "root", "--headless"},
		runOnce, exit.Exit, stdout, stderr, nil, nil)

	// Check that cursor show sequence is in output
	output := stdout.String()
	if !strings.Contains(output, "\x1b[?25h") {
		t.Errorf("Expected cursor show sequence (\\x1b[?25h) in output, got: %q", output)
	}

	// Should have zero exit code for success
	if code != 0 {
		t.Errorf("Expected zero exit code, got %d", code)
	}
}

func TestCursorVisibleAfterProgressCancellation(t *testing.T) {
	// Test that cursor is visible when progress is cancelled via context
	buffer := &bytes.Buffer{}
	progress := ui.NewProgress(ui.ProgressConfig{
		Writer: buffer,
		State:  "opencode running",
	})

	// Simulate context cancellation
	ctx, cancel := context.WithCancel(context.Background())
	go progress.Run(ctx)
	cancel()

	// Give some time for cancellation to be processed
	time.Sleep(10 * time.Millisecond)

	output := buffer.String()
	// Should contain cursor show sequence after cancellation
	if !strings.Contains(output, "\x1b[?25h") {
		t.Errorf("Expected cursor show sequence (\\x1b[?25h) after context cancellation, got: %q", output)
	}
}
