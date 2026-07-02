// Package agentrun holds shared helpers for the CLI agent-backend adapters
// (claude, codex, kimi). These types and functions were previously
// copy-pasted byte-for-byte across each adapter package.
package agentrun

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// CommandSpec describes a subprocess invocation for a CLI agent backend.
type CommandSpec struct {
	Binary string
	Args   []string
	Env    []string
	Dir    string
	Stdout io.Writer
	Stderr io.Writer
}

// RunCommand executes the described command, honoring the context's
// cancellation/deadline. binaryName is used only to build a clear error when
// Binary is empty (e.g. "claude", "codex", "kimi").
func RunCommand(ctx context.Context, binaryName string, spec CommandSpec) error {
	if strings.TrimSpace(spec.Binary) == "" {
		return errors.New(binaryName + " binary is required")
	}
	cmd := exec.CommandContext(ctx, spec.Binary, spec.Args...)
	if strings.TrimSpace(spec.Dir) != "" {
		cmd.Dir = spec.Dir
	}
	if len(spec.Env) > 0 {
		cmd.Env = append(os.Environ(), spec.Env...)
	}
	cmd.Stdout = spec.Stdout
	cmd.Stderr = spec.Stderr
	err := cmd.Run()
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if err != nil && errors.Is(ctx.Err(), context.Canceled) {
		return context.Canceled
	}
	return err
}

// LineWriter is a line-buffered writer: it mirrors bytes to target (if non-nil)
// and invokes emit once per completed line. Flush emits any trailing partial
// line. It is safe for concurrent use.
type LineWriter struct {
	target  io.Writer
	emit    func(string)
	mu      sync.Mutex
	pending strings.Builder
}

// NewLineWriter returns a LineWriter that mirrors to target and calls emit
// per line.
func NewLineWriter(target io.Writer, emit func(string)) *LineWriter {
	return &LineWriter{target: target, emit: emit}
}

// Write implements io.Writer.
func (w *LineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.target != nil {
		if _, err := w.target.Write(p); err != nil {
			return 0, err
		}
	}
	if len(p) == 0 {
		return 0, nil
	}
	w.consumeLocked(string(p))
	return len(p), nil
}

// Flush emits any buffered partial line and resets the buffer.
func (w *LineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending.Len() == 0 {
		return
	}
	if w.emit != nil {
		w.emit(w.pending.String())
	}
	w.pending.Reset()
}

func (w *LineWriter) consumeLocked(chunk string) {
	for _, r := range chunk {
		if r == '\n' {
			if w.emit != nil {
				w.emit(w.pending.String())
			}
			w.pending.Reset()
			continue
		}
		w.pending.WriteRune(r)
	}
}
