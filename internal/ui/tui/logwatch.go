package tui

import (
	"context"
	"os"
	"time"
)

type LogWatchTicker interface {
	C() <-chan time.Time
	Stop()
}

type LogWatchConfig struct {
	Path   string
	Ticker LogWatchTicker
	Emit   func(OutputMsg)
}

type LogWatcher struct {
	config LogWatchConfig
}

type realLogWatchTicker struct {
	ticker *time.Ticker
}

func (r realLogWatchTicker) C() <-chan time.Time {
	return r.ticker.C
}

func (r realLogWatchTicker) Stop() {
	if r.ticker != nil {
		r.ticker.Stop()
	}
}

func NewLogWatcher(config LogWatchConfig) *LogWatcher {
	return &LogWatcher{config: config}
}

func (w *LogWatcher) Run(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	config := w.config
	if config.Emit == nil {
		config.Emit = func(OutputMsg) {}
	}
	if config.Ticker == nil {
		config.Ticker = realLogWatchTicker{ticker: time.NewTicker(2 * time.Second)}
	}
	defer config.Ticker.Stop()

	lastSize := fileSize(config.Path)
	for {
		select {
		case <-ctx.Done():
			return
		case <-config.Ticker.C():
			currentSize := fileSize(config.Path)
			if currentSize > lastSize {
				lastSize = currentSize
				config.Emit(OutputMsg{})
			}
		}
	}
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
