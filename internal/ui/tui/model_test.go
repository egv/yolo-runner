package tui

import (
	"strings"
	"testing"
	"time"

	"yolo-runner/internal/runner"
)

func TestModelRendersTaskAndPhase(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })
	updated, _ := m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		Phase:     "running",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	view := m.View()
	if !strings.Contains(view, "task-1 - Example Task") {
		t.Fatalf("expected task id and title in view, got %q", view)
	}
	if !strings.Contains(view, "running") {
		t.Fatalf("expected phase in view, got %q", view)
	}
	if !strings.Contains(view, "last output 5s") {
		t.Fatalf("expected last output age in view, got %q", view)
	}
}

func TestSpinnerAdvancesOnOutput(t *testing.T) {
	m := NewModel(func() time.Time { return time.Unix(0, 0) })
	updated, _ := m.Update(OutputMsg{})
	m = updated.(Model)
	first := m.View()
	updated, _ = m.Update(OutputMsg{})
	m = updated.(Model)
	second := m.View()

	if first == second {
		t.Fatalf("expected spinner to advance, got %q", second)
	}
}
