package main

import (
	"strings"
	"sync"

	"github.com/egv/yolo-runner/internal/ui/tui"
)

type tuiLogWriter struct {
	program tuiProgram
	mu      sync.Mutex
	buffer  strings.Builder
}

func newTUILogWriter(program tuiProgram) *tuiLogWriter {
	return &tuiLogWriter{program: program}
}

func (w *tuiLogWriter) Write(p []byte) (int, error) {
	if w == nil {
		return len(p), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buffer.Write(p)
	buffered := normalizeLineBreaks(w.buffer.String())
	lines := strings.Split(buffered, "\n")
	for i := 0; i < len(lines)-1; i++ {
		w.emit(strings.TrimRight(lines[i], "\r"))
	}
	remaining := lines[len(lines)-1]
	w.buffer.Reset()
	if remaining != "" {
		w.buffer.WriteString(remaining)
	}
	return len(p), nil
}

func (w *tuiLogWriter) Flush() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := normalizeLineBreaks(w.buffer.String())
	w.buffer.Reset()
	if remaining == "" {
		return
	}
	lines := strings.Split(remaining, "\n")
	for i, line := range lines {
		if i == len(lines)-1 && line == "" {
			continue
		}
		w.emit(strings.TrimRight(line, "\r"))
	}
}

func (w *tuiLogWriter) emit(line string) {
	if w.program == nil {
		return
	}
	w.program.SendInput(tui.AppendLogMsg{Line: line})
}

func normalizeLineBreaks(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}
