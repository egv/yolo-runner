package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeTicker struct {
	ch     chan time.Time
	closed bool
}

func newFakeTicker() *fakeTicker {
	return &fakeTicker{ch: make(chan time.Time)}
}

func (f *fakeTicker) C() <-chan time.Time {
	return f.ch
}

func (f *fakeTicker) Stop() {
	if f.closed {
		return
	}
	close(f.ch)
	f.closed = true
}

func (f *fakeTicker) Tick(now time.Time) {
	f.ch <- now
}

func TestLogWatcherEmitsOnGrowth(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "issue-1.jsonl")
	if err := os.WriteFile(logPath, []byte("start"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	ticker := newFakeTicker()
	events := make(chan OutputMsg, 1)
	watcher := NewLogWatcher(LogWatchConfig{
		Path:   logPath,
		Ticker: ticker,
		Emit: func(msg OutputMsg) {
			events <- msg
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		watcher.Run(ctx)
		close(done)
	}()

	ticker.Tick(time.Unix(1, 0))

	select {
	case <-events:
		t.Fatalf("unexpected output event")
	default:
	}

	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	if _, err := file.Write([]byte("more")); err != nil {
		_ = file.Close()
		t.Fatalf("append file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}

	ticker.Tick(time.Unix(2, 0))

	select {
	case <-events:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected output event")
	}

	cancel()
	ticker.Stop()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("watcher did not stop")
	}
}

func TestLogWatcherNoGrowthNoEvent(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "issue-2.jsonl")
	if err := os.WriteFile(logPath, []byte("start"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	ticker := newFakeTicker()
	events := make(chan OutputMsg, 1)
	watcher := NewLogWatcher(LogWatchConfig{
		Path:   logPath,
		Ticker: ticker,
		Emit: func(msg OutputMsg) {
			events <- msg
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		watcher.Run(ctx)
		close(done)
	}()

	ticker.Tick(time.Unix(2, 0))

	select {
	case <-events:
		t.Fatalf("unexpected output event")
	default:
	}

	cancel()
	ticker.Stop()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("watcher did not stop")
	}
}
