package agentrun

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestLineWriterEmitsPerLineAndMirrorsToTarget(t *testing.T) {
	var target bytes.Buffer
	got := make([]string, 0, 3)
	w := NewLineWriter(&target, func(line string) { got = append(got, line) })

	if _, err := w.Write([]byte("alpha\nbe")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("expected one emitted line %q, got %v", "alpha", got)
	}
	if target.String() != "alpha\nbe" {
		t.Fatalf("mirror target = %q, want %q", target.String(), "alpha\\nbe")
	}

	w.Flush()
	if len(got) != 2 || got[1] != "be" {
		t.Fatalf("flush should emit trailing partial %q, got %v", "be", got)
	}
}

func TestLineWriterNilTargetIsAllowed(t *testing.T) {
	got := make([]string, 0, 1)
	w := NewLineWriter(nil, func(line string) { got = append(got, line) })
	if _, err := w.Write([]byte("x\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(got) != 1 || got[0] != "x" {
		t.Fatalf("emit got %v, want [x]", got)
	}
}

func TestRunCommandEmptyBinaryErrors(t *testing.T) {
	err := RunCommand(context.Background(), "claude", CommandSpec{})
	if err == nil || !strings.Contains(err.Error(), "claude binary is required") {
		t.Fatalf("expected empty-binary error, got %v", err)
	}
}

func TestRunCommandRespectsDeadline(t *testing.T) {
	// "sleep 5" should be killed by a short deadline and surface DeadlineExceeded.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := RunCommand(ctx, "test", CommandSpec{
		Binary: "sleep",
		Args:   []string{"5"},
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if err != context.DeadlineExceeded {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}
