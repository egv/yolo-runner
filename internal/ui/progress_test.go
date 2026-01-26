package ui

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeProgressTicker struct {
	ch     chan time.Time
	closed bool
}

func newFakeProgressTicker() *fakeProgressTicker {
	return &fakeProgressTicker{ch: make(chan time.Time)}
}

func (f *fakeProgressTicker) C() <-chan time.Time {
	return f.ch
}

func (f *fakeProgressTicker) Stop() {
	if f.closed {
		return
	}
	close(f.ch)
	f.closed = true
}

func (f *fakeProgressTicker) Tick(now time.Time) {
	f.ch <- now
}

func waitForOutput(t *testing.T, buf *bytes.Buffer) string {
	return waitForOutputAfter(t, buf, 0)
}

func waitForOutputAfter(t *testing.T, buf *bytes.Buffer, prevLen int) string {
	t.Helper()
	for i := 0; i < 50; i++ {
		if buf.Len() > prevLen {
			return buf.String()
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected output")
	return ""
}

func lastRender(output string) string {
	parts := strings.Split(output, "\r")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func TestProgressRendersHeartbeatWithStateAndAge(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "issue-1.jsonl")
	if err := os.WriteFile(logPath, []byte("start"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	baseTime := time.Date(2026, 1, 20, 10, 0, 0, 0, time.UTC)
	if err := os.Chtimes(logPath, baseTime, baseTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	current := baseTime
	now := func() time.Time { return current }
	buffer := &bytes.Buffer{}
	ticker := newFakeProgressTicker()
	progress := NewProgress(ProgressConfig{
		Writer:  buffer,
		State:   "opencode running",
		LogPath: logPath,
		Ticker:  ticker,
		Now:     now,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		progress.Run(ctx)
		close(done)
	}()

	current = baseTime.Add(5 * time.Second)
	ticker.Tick(current)
	output := waitForOutput(t, buffer)

	if !strings.Contains(output, "\r") {
		t.Fatalf("expected carriage return output, got %q", output)
	}
	if strings.Contains(output, "\n") {
		t.Fatalf("did not expect newline before finish, got %q", output)
	}
	if !strings.Contains(output, "opencode running") {
		t.Fatalf("expected state in output, got %q", output)
	}
	if !strings.Contains(output, "last output 5s") {
		t.Fatalf("expected last output age in output, got %q", output)
	}

	cancel()
	ticker.Stop()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("progress did not stop")
	}
}

func TestProgressResetsStaleLastOutputOnStart(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "issue-stale.jsonl")
	if err := os.WriteFile(logPath, []byte("start"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	baseTime := time.Date(2026, 1, 20, 10, 0, 0, 0, time.UTC)
	staleTime := baseTime.Add(-2 * time.Hour)
	if err := os.Chtimes(logPath, staleTime, staleTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	current := baseTime
	now := func() time.Time { return current }
	buffer := &bytes.Buffer{}
	ticker := newFakeProgressTicker()
	progress := NewProgress(ProgressConfig{
		Writer:  buffer,
		State:   "opencode running",
		LogPath: logPath,
		Ticker:  ticker,
		Now:     now,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		progress.Run(ctx)
		close(done)
	}()

	current = baseTime
	ticker.Tick(current)
	output := waitForOutput(t, buffer)
	line := lastRender(output)
	if !strings.Contains(line, "last output 0s") {
		t.Fatalf("expected stale last output reset to 0s, got %q", line)
	}

	cancel()
	ticker.Stop()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("progress did not stop")
	}
}

func TestProgressSpinnerAdvancesOnNewBytes(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "issue-2.jsonl")
	if err := os.WriteFile(logPath, []byte("start"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	baseTime := time.Date(2026, 1, 20, 10, 0, 0, 0, time.UTC)
	if err := os.Chtimes(logPath, baseTime, baseTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	current := baseTime
	now := func() time.Time { return current }
	buffer := &bytes.Buffer{}
	ticker := newFakeProgressTicker()
	progress := NewProgress(ProgressConfig{
		Writer:  buffer,
		State:   "opencode running",
		LogPath: logPath,
		Ticker:  ticker,
		Now:     now,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		progress.Run(ctx)
		close(done)
	}()

	current = baseTime
	ticker.Tick(current)
	firstOutput := waitForOutput(t, buffer)
	firstLen := len(firstOutput)
	firstLine := lastRender(firstOutput)

	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	if _, err := file.Write([]byte("more")); err != nil {
		_ = file.Close()
		t.Fatalf("append log: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close log: %v", err)
	}

	current = baseTime.Add(2 * time.Second)
	ticker.Tick(current)
	secondOutput := waitForOutputAfter(t, buffer, firstLen)
	secondLine := lastRender(secondOutput)

	if firstLine == secondLine {
		t.Fatalf("expected spinner to advance, got %q", secondLine)
	}
	if len(firstLine) == 0 || len(secondLine) == 0 {
		t.Fatalf("expected rendered output")
	}
	if firstLine[0] == secondLine[0] {
		t.Fatalf("expected spinner char to change")
	}

	cancel()
	ticker.Stop()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("progress did not stop")
	}
}

func TestProgressFinishPrintsCRLF(t *testing.T) {
	buffer := &bytes.Buffer{}
	progress := NewProgress(ProgressConfig{
		Writer: buffer,
		State:  "opencode running",
		Ticker: newFakeProgressTicker(),
	})

	progress.Finish(nil)

	expectedOutput := "\r\nOpenCode finished\r\n\x1b[?25h"
	if buffer.String() != expectedOutput {
		t.Fatalf("unexpected finish output: %q, expected: %q", buffer.String(), expectedOutput)
	}
}

func TestProgressClearsShorterAgeLine(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "issue-3.jsonl")
	if err := os.WriteFile(logPath, []byte("start"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	baseTime := time.Date(2026, 1, 20, 10, 0, 0, 0, time.UTC)
	if err := os.Chtimes(logPath, baseTime, baseTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	current := baseTime
	now := func() time.Time { return current }
	buffer := &bytes.Buffer{}
	ticker := newFakeProgressTicker()
	progress := NewProgress(ProgressConfig{
		Writer:  buffer,
		State:   "opencode running",
		LogPath: logPath,
		Ticker:  ticker,
		Now:     now,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		progress.Run(ctx)
		close(done)
	}()

	current = baseTime.Add(12 * time.Second)
	ticker.Tick(current)
	firstOutput := waitForOutput(t, buffer)
	firstLine := lastRender(firstOutput)
	firstLen := len(firstLine)
	if !strings.Contains(firstLine, "last output 12s") {
		t.Fatalf("expected last output age in output, got %q", firstLine)
	}

	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	if _, err := file.Write([]byte("more")); err != nil {
		_ = file.Close()
		t.Fatalf("append log: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close log: %v", err)
	}

	current = baseTime.Add(13 * time.Second)
	if err := os.Chtimes(logPath, current, current); err != nil {
		t.Fatalf("chtimes update: %v", err)
	}
	prevLen := len(firstOutput)
	ticker.Tick(current)
	secondOutput := waitForOutputAfter(t, buffer, prevLen)
	secondLine := lastRender(secondOutput)
	if !strings.Contains(secondLine, "last output 0s") {
		t.Fatalf("expected last output age reset, got %q", secondLine)
	}
	if len(secondLine) < firstLen {
		t.Fatalf("expected render to clear shorter line, got lengths %d then %d", firstLen, len(secondLine))
	}

	cancel()
	ticker.Stop()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("progress did not stop")
	}
}

func TestProgressFinishPrintsFinalLine(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "issue-3.jsonl")
	if err := os.WriteFile(logPath, []byte("start"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	current := time.Date(2026, 1, 20, 10, 0, 0, 0, time.UTC)
	now := func() time.Time { return current }
	buffer := &bytes.Buffer{}
	progress := NewProgress(ProgressConfig{
		Writer:  buffer,
		State:   "opencode running",
		LogPath: logPath,
		Now:     now,
	})

	progress.Finish(nil)
	output := buffer.String()
	if !strings.Contains(output, "\r\nOpenCode finished\r\n") {
		t.Fatalf("expected finished line, got %q", output)
	}
	if !strings.Contains(output, "\x1b[?25h") {
		t.Fatalf("expected cursor show sequence, got %q", output)
	}
}

func TestProgressFinishResetsLinePosition(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "issue-4.jsonl")
	if err := os.WriteFile(logPath, []byte("start"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	current := time.Date(2026, 1, 20, 10, 0, 0, 0, time.UTC)
	now := func() time.Time { return current }
	buffer := &bytes.Buffer{}
	progress := NewProgress(ProgressConfig{
		Writer:  buffer,
		State:   "opencode running",
		LogPath: logPath,
		Now:     now,
	})

	progress.renderLocked(current)
	progress.Finish(nil)

	output := buffer.String()
	if !strings.Contains(output, "\r\nOpenCode finished\r\n") {
		t.Fatalf("expected finish to start on new line, got %q", output)
	}
	if !strings.Contains(output, "\x1b[?25h") {
		t.Fatalf("expected cursor show sequence, got %q", output)
	}
}

func TestProgressSpinnerAdvancesOnTimer(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "issue-timer.jsonl")
	if err := os.WriteFile(logPath, []byte("start"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	baseTime := time.Date(2026, 1, 20, 10, 0, 0, 0, time.UTC)
	if err := os.Chtimes(logPath, baseTime, baseTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	current := baseTime
	now := func() time.Time { return current }
	buffer := &bytes.Buffer{}
	ticker := newFakeProgressTicker()
	progress := NewProgress(ProgressConfig{
		Writer:  buffer,
		State:   "opencode running",
		LogPath: logPath,
		Ticker:  ticker,
		Now:     now,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		progress.Run(ctx)
		close(done)
	}()

	// First tick - should show initial spinner
	current = baseTime
	ticker.Tick(current)
	firstOutput := waitForOutput(t, buffer)
	firstLen := len(firstOutput)
	firstLine := lastRender(firstOutput)

	// Second tick - should advance spinner even without log file growth
	current = baseTime.Add(1 * time.Second)
	ticker.Tick(current)
	secondOutput := waitForOutputAfter(t, buffer, firstLen)
	secondLine := lastRender(secondOutput)

	// Verify spinner advanced
	if firstLine == secondLine {
		t.Fatalf("expected spinner to advance on timer, got same line: %q", secondLine)
	}
	if len(firstLine) == 0 || len(secondLine) == 0 {
		t.Fatalf("expected rendered output")
	}
	if firstLine[0] == secondLine[0] {
		t.Fatalf("expected spinner char to change on timer, got %q then %q", firstLine[0], secondLine[0])
	}

	// Third tick - should advance spinner again
	current = baseTime.Add(2 * time.Second)
	ticker.Tick(current)
	thirdOutput := waitForOutputAfter(t, buffer, len(secondOutput))
	thirdLine := lastRender(thirdOutput)

	if secondLine[0] == thirdLine[0] {
		t.Fatalf("expected spinner char to change again on timer, got %q then %q", secondLine[0], thirdLine[0])
	}

	cancel()
	ticker.Stop()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("progress did not stop")
	}
}
