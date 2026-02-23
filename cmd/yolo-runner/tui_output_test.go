//go:build legacy

package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/egv/yolo-runner/internal/runner"
	"github.com/egv/yolo-runner/internal/ui/tui"
)

type captureTUIProgram struct {
	inputs []tea.Msg
}

func (c *captureTUIProgram) Start() error {
	return nil
}

func (c *captureTUIProgram) Send(event runner.Event) {
	_ = event
}

func (c *captureTUIProgram) SendInput(msg tea.Msg) {
	c.inputs = append(c.inputs, msg)
}

func (c *captureTUIProgram) Quit() {
}

func TestTUILogWriterEmitsOnCarriageReturns(t *testing.T) {
	program := &captureTUIProgram{}
	writer := newTUILogWriter(program)

	if _, err := writer.Write([]byte("first\rsecond\r")); err != nil {
		t.Fatalf("write: %v", err)
	}

	if len(program.inputs) != 2 {
		t.Fatalf("expected 2 log messages, got %d", len(program.inputs))
	}

	first, ok := program.inputs[0].(tui.AppendLogMsg)
	if !ok {
		t.Fatalf("expected first message to be AppendLogMsg, got %T", program.inputs[0])
	}
	if first.Line != "first" {
		t.Fatalf("expected first line to be %q, got %q", "first", first.Line)
	}

	second, ok := program.inputs[1].(tui.AppendLogMsg)
	if !ok {
		t.Fatalf("expected second message to be AppendLogMsg, got %T", program.inputs[1])
	}
	if second.Line != "second" {
		t.Fatalf("expected second line to be %q, got %q", "second", second.Line)
	}
}
