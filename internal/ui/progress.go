package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

type ProgressTicker interface {
	C() <-chan time.Time
	Stop()
}

type ProgressConfig struct {
	Writer  io.Writer
	State   string
	LogPath string
	Ticker  ProgressTicker
	Now     func() time.Time
}

type Progress struct {
	config        ProgressConfig
	spinnerIndex  int
	lastSize      int64
	lastOutput    time.Time
	state         string
	finished      bool
	lastRenderLen int
	mu            sync.Mutex
}

type realProgressTicker struct {
	ticker *time.Ticker
}

func (r realProgressTicker) C() <-chan time.Time {
	return r.ticker.C
}

func (r realProgressTicker) Stop() {
	if r.ticker != nil {
		r.ticker.Stop()
	}
}

var spinnerFrames = []string{"-", "\\", "|", "/"}

func NewProgress(config ProgressConfig) *Progress {
	if config.Writer == nil {
		config.Writer = io.Discard
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Ticker == nil {
		config.Ticker = realProgressTicker{ticker: time.NewTicker(time.Second)}
	}
	lastOutput := config.Now()
	if config.LogPath != "" {
		if modTime, err := fileModTime(config.LogPath); err == nil {
			lastOutput = modTime
		}
	}
	return &Progress{
		config:       config,
		lastSize:     fileSize(config.LogPath),
		lastOutput:   lastOutput,
		state:        config.State,
		spinnerIndex: 1,
	}
}

func (p *Progress) Run(ctx context.Context) {
	if p == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	ticker := p.config.Ticker
	p.mu.Unlock()
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C():
			p.render(now)
		}
	}
}

func (p *Progress) SetState(state string) {
	if p == nil || state == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if state == p.state {
		return
	}
	fmt.Fprint(p.config.Writer, "\n")
	p.state = state
	p.renderLocked(p.config.Now())
}

func (p *Progress) Finish(err error) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished {
		return
	}
	p.finished = true
	fmt.Fprint(p.config.Writer, "\r\nOpenCode finished\n")
}

func (p *Progress) render(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.renderLocked(now)
}

func (p *Progress) renderLocked(now time.Time) {
	if p.finished {
		return
	}
	if p.config.LogPath != "" {
		currentSize := fileSize(p.config.LogPath)
		if currentSize > p.lastSize {
			p.lastSize = currentSize
			p.spinnerIndex = (p.spinnerIndex + 1) % len(spinnerFrames)
			if modTime, err := fileModTime(p.config.LogPath); err == nil {
				p.lastOutput = modTime
			} else {
				p.lastOutput = now
			}
		}
	}
	age := "n/a"
	if !p.lastOutput.IsZero() {
		seconds := int(now.Sub(p.lastOutput).Round(time.Second).Seconds())
		if seconds < 0 {
			seconds = 0
		}
		age = fmt.Sprintf("%ds", seconds)
	}
	spinner := spinnerFrames[p.spinnerIndex%len(spinnerFrames)]
	line := fmt.Sprintf("%s %s - last output %s", spinner, p.state, age)
	pad := ""
	lineLen := len(line)
	if p.lastRenderLen > lineLen {
		pad = strings.Repeat(" ", p.lastRenderLen-lineLen)
	}
	fmt.Fprintf(p.config.Writer, "\r%s%s", line, pad)
	p.lastRenderLen = lineLen
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func fileModTime(path string) (time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}
